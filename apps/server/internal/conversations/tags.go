package conversations

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
)

// Limits of tags.
const (
	MaxTags             = 500 // per workspace; GET /tags returns all of them
	MaxConversationTags = 20
	MaxTagNameLength    = 40
)

// TagColors are the palette keys a tag may use, as the tags.color CHECK.
var TagColors = []string{"gray", "teal", "amber", "red", "violet", "blue", "green"}

// Tag labels conversations (docs/04 §3.1, §5.4).
type Tag struct {
	ID        uuid.UUID
	Name      string
	Color     string
	CreatedAt time.Time
}

// TagInput is a new tag; an empty Color is gray.
type TagInput struct {
	Name  string
	Color string
}

// Tags lists every tag of the workspace by name.
func (s *Service) Tags(ctx context.Context, workspaceID uuid.UUID) ([]Tag, error) {
	var out []Tag
	err := s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		rows, err := q.ListTags(ctx, store.ListTagsParams{WorkspaceID: workspaceID, Lim: MaxTags})
		for _, r := range rows {
			out = append(out, tagFrom(r))
		}
		return err
	})
	if err != nil {
		return nil, wrap(err, "list tags")
	}
	return out, nil
}

// CreateTag adds a tag. A name already taken, compared case-insensitively,
// is ErrTagExists.
func (s *Service) CreateTag(ctx context.Context, workspaceID, userID uuid.UUID, in TagInput) (Tag, error) {
	name := strings.Join(strings.Fields(in.Name), " ")
	color := in.Color
	if color == "" {
		color = "gray"
	}
	switch {
	case name == "":
		return Tag{}, fmt.Errorf("%w: name is required", ErrInvalid)
	case utf8.RuneCountInString(name) > MaxTagNameLength:
		return Tag{}, fmt.Errorf("%w: name must be at most %d characters", ErrInvalid, MaxTagNameLength)
	case !slices.Contains(TagColors, color):
		return Tag{}, fmt.Errorf("%w: unknown color %q", ErrInvalid, color)
	}
	id, err := newID()
	if err != nil {
		return Tag{}, err
	}
	var out Tag
	err = s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		n, err := q.CountTags(ctx, workspaceID)
		if err != nil {
			return err
		}
		if n >= MaxTags {
			return fmt.Errorf("%w: a workspace has at most %d tags", ErrInvalid, MaxTags)
		}
		rows, err := q.CreateTag(ctx, store.CreateTagParams{ID: id, WorkspaceID: workspaceID, Name: name, Color: color, CreatedBy: &userID})
		if err != nil {
			return err
		}
		out = tagFrom(rows)
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Tag{}, ErrTagExists
		}
		return Tag{}, wrap(err, "create tag")
	}
	return out, nil
}

// SetTags replaces the tags of a conversation. Anyone who may tag works on
// any visible conversation, closed ones included: tagging classifies the
// conversation and changes nothing for the customer.
func (s *Service) SetTags(
	ctx context.Context, workspaceID uuid.UUID, actorID, conversationID uuid.UUID, tagIDs []uuid.UUID, version *int,
) (Conversation, error) {
	if tagIDs == nil {
		tagIDs = []uuid.UUID{} // NULL would make "NOT (tag_id = ANY(keep))" remove nothing
	}
	ids := slices.Clone(tagIDs)
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	switch {
	case len(slices.Compact(ids)) != len(tagIDs):
		return Conversation{}, fmt.Errorf("%w: tag ids must be unique", ErrInvalid)
	case len(tagIDs) > MaxConversationTags:
		return Conversation{}, fmt.Errorf("%w: at most %d tags per conversation", ErrInvalid, MaxConversationTags)
	}
	var out Conversation
	err := s.tx(ctx, workspaceID, func(q *store.Queries, tx pgx.Tx) error {
		row, err := q.LockConversation(ctx, store.LockConversationParams{WorkspaceID: workspaceID, ID: conversationID})
		if err != nil {
			return err
		}
		if version != nil && *version != int(row.Version) {
			return ErrConflict
		}
		if n, err := q.CountTagsByID(ctx, store.CountTagsByIDParams{WorkspaceID: workspaceID, Ids: tagIDs}); err != nil {
			return err
		} else if n != int64(len(tagIDs)) {
			return fmt.Errorf("%w: a tag does not exist", ErrInvalid)
		}
		removed, err := q.RemoveConversationTags(ctx, store.RemoveConversationTagsParams{
			WorkspaceID: workspaceID, ConversationID: conversationID, Keep: tagIDs,
		})
		if err != nil {
			return err
		}
		added, err := q.AddConversationTags(ctx, store.AddConversationTagsParams{
			WorkspaceID: workspaceID, ConversationID: conversationID, CreatedBy: &actorID, TagIds: tagIDs,
		})
		if err != nil {
			return err
		}
		if removed+added > 0 {
			if err := q.BumpConversation(ctx, store.BumpConversationParams{WorkspaceID: workspaceID, ID: conversationID}); err != nil {
				return err
			}
			if err := outbox.Add(ctx, tx, workspaceID, "conversation.tags_changed", map[string]any{
				"conversation_id": conversationID,
			}); err != nil {
				return err
			}
		}
		out, err = getConversation(ctx, q, workspaceID, actorID, conversationID)
		return err
	})
	if err != nil {
		return Conversation{}, wrap(err, "set tags")
	}
	return out, nil
}

func tagFrom(t store.Tag) Tag {
	return Tag{ID: t.ID, Name: t.Name, Color: t.Color, CreatedAt: t.CreatedAt}
}
