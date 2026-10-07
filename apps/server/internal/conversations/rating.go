package conversations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
)

// The customer's rating of a resolved conversation (docs/04 §5.4, W-05).
// The widget asks for it once the conversation is resolved; rating again
// replaces it. Telegram's buttons come later.

// ErrConversationOpen rejects a rating of a conversation that is not
// resolved: it may have reopened since the widget asked.
var ErrConversationOpen = errors.New("conversations: conversation is not resolved")

// MaxRatingComment bounds the comment of a rating, in characters.
const MaxRatingComment = 1000

// Who resolved a rated conversation (Rating.ResolvedBy).
const (
	ResolvedByHuman = "human"
	// ResolvedByAI: only the bot replied in the conversation.
	ResolvedByAI = "ai"
)

// Rating is the customer's rating of a conversation.
type Rating struct {
	Score      int     // 1 to 5
	Comment    *string // nil without one
	ResolvedBy string  // ResolvedByHuman or ResolvedByAI
	RatedAt    time.Time
}

// VisitorConversation is a widget visitor's newest conversation as they
// see it: whether it is resolved, and their rating.
type VisitorConversation struct {
	ID       uuid.UUID
	Resolved bool // resolved or closed
	Rating   *Rating
	TopicID  *uuid.UUID
}

// RateVisitorConversation stores the visitor's rating of a resolved
// conversation of their chat and tells the panel (conversation.rated).
// ErrNotFound for a conversation of anyone else, ErrConversationOpen for
// one not resolved.
func (s *Service) RateVisitorConversation(
	ctx context.Context, workspaceID uuid.UUID, chat VisitorChat, id uuid.UUID, score int, comment string,
) (Rating, error) {
	if score < 1 || score > 5 {
		return Rating{}, fmt.Errorf("%w: score must be 1 to 5", ErrInvalid)
	}
	comment = strings.TrimSpace(comment)
	if utf8.RuneCountInString(comment) > MaxRatingComment {
		return Rating{}, fmt.Errorf("%w: comment must be at most %d characters", ErrInvalid, MaxRatingComment)
	}
	r := Rating{Score: score, RatedAt: s.now()}
	if comment != "" {
		r.Comment = &comment
	}
	err := s.tx(ctx, workspaceID, func(q *store.Queries, tx pgx.Tx) error {
		row, err := q.LockVisitorConversation(ctx, store.LockVisitorConversationParams{
			WorkspaceID: workspaceID, ID: id, ChannelID: &chat.ChannelID, Thread: &chat.Thread,
		})
		if err != nil {
			return err
		}
		if !isSettled(Status(row.Status)) {
			return ErrConversationOpen
		}
		by, err := q.ConversationRepliers(ctx, store.ConversationRepliersParams{WorkspaceID: workspaceID, ConversationID: id})
		if err != nil {
			return err
		}
		r.ResolvedBy = ResolvedByHuman
		if by.Bot && !by.Operator {
			r.ResolvedBy = ResolvedByAI
		}
		if err := q.SetConversationRating(ctx, store.SetConversationRatingParams{
			Rating:  int16(score), //nolint:gosec // G115: 1 to 5, checked above
			Comment: r.Comment, ResolvedBy: r.ResolvedBy, RatedAt: r.RatedAt, WorkspaceID: workspaceID, ID: id,
		}); err != nil {
			return err
		}
		return outbox.Add(ctx, tx, workspaceID, "conversation.rated", map[string]any{"conversation_id": id})
	})
	if err != nil {
		return Rating{}, wrap(err, "rate conversation")
	}
	return r, nil
}

// visitorConversation reads the visitor's newest conversation; nil before
// their first message.
func visitorConversation(ctx context.Context, q *store.Queries, workspaceID uuid.UUID, chat VisitorChat) (*VisitorConversation, error) {
	row, err := q.VisitorConversation(ctx, store.VisitorConversationParams{
		WorkspaceID: workspaceID, ChannelID: &chat.ChannelID, Thread: &chat.Thread,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &VisitorConversation{ID: row.ID, Resolved: isSettled(Status(row.Status)), Rating: ratingFrom(row), TopicID: row.TopicID}, nil
}

func ratingFrom(c store.Conversation) *Rating {
	if !c.Rating.Valid || c.RatedAt == nil || c.RatingResolvedBy == nil {
		return nil
	}
	return &Rating{Score: int(c.Rating.Int16), Comment: c.RatingComment, ResolvedBy: *c.RatingResolvedBy, RatedAt: *c.RatedAt}
}
