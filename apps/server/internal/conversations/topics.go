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
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
)

// Topics: what a question is about (docs/02 §7.3). The customer picks one
// before writing (the Telegram bot's buttons, the widget's chips) or an
// operator sets it. A topic gives the conversation its priority, narrows
// automatic distribution to its members and gives tasks made from the
// conversation a default queue.

// Limits of topics: a topic is a button, and a bot's message holds a few.
const (
	MaxTopics          = 20
	MaxTopicNameLength = 40
	MaxTopicPriority   = 3
)

// ErrTopicExists is a topic whose name is taken.
var ErrTopicExists = errors.New("conversations: topic name is taken")

// Topic is a topic of the workspace.
type Topic struct {
	ID       uuid.UUID
	Name     string
	Color    string
	Position int
	// Priority is the conversations.priority (0–3) of its conversations.
	Priority    int
	TaskQueueID *uuid.UUID
	// MemberIDs get its conversations from automatic distribution; empty:
	// everyone.
	MemberIDs []uuid.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TopicInput is a new or changed topic; an empty Color is gray.
type TopicInput struct {
	Name        string
	Color       string
	Priority    int
	TaskQueueID *uuid.UUID
	MemberIDs   []uuid.UUID
}

func (in TopicInput) valid() (TopicInput, error) {
	in.Name = strings.Join(strings.Fields(in.Name), " ")
	if in.Color == "" {
		in.Color = "gray"
	}
	ids := slices.Clone(in.MemberIDs)
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	switch {
	case in.Name == "":
		return in, fmt.Errorf("%w: name is required", ErrInvalid)
	case utf8.RuneCountInString(in.Name) > MaxTopicNameLength:
		return in, fmt.Errorf("%w: name must be at most %d characters", ErrInvalid, MaxTopicNameLength)
	case !slices.Contains(TagColors, in.Color):
		return in, fmt.Errorf("%w: unknown color %q", ErrInvalid, in.Color)
	case in.Priority < 0 || in.Priority > MaxTopicPriority:
		return in, fmt.Errorf("%w: priority must be between 0 and %d", ErrInvalid, MaxTopicPriority)
	case len(slices.Compact(ids)) != len(in.MemberIDs):
		return in, fmt.Errorf("%w: member ids must be unique", ErrInvalid)
	}
	if in.MemberIDs == nil {
		in.MemberIDs = []uuid.UUID{}
	}
	return in, nil
}

// Topics lists the topics of the workspace in the order of the buttons.
func (s *Service) Topics(ctx context.Context, workspaceID uuid.UUID) ([]Topic, error) {
	var out []Topic
	err := s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		var err error
		out, err = listTopics(ctx, q, workspaceID)
		return err
	})
	if err != nil {
		return nil, wrap(err, "list topics")
	}
	return out, nil
}

// CreateTopic adds a topic after the others. A name already taken,
// compared case-insensitively, is ErrTopicExists.
func (s *Service) CreateTopic(ctx context.Context, workspaceID uuid.UUID, in TopicInput) (Topic, error) {
	in, err := in.valid()
	if err != nil {
		return Topic{}, err
	}
	id, err := newID()
	if err != nil {
		return Topic{}, err
	}
	var out Topic
	err = s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		n, err := q.CountTopics(ctx, workspaceID)
		if err != nil {
			return err
		}
		if n >= MaxTopics {
			return fmt.Errorf("%w: a workspace has at most %d topics", ErrInvalid, MaxTopics)
		}
		row, err := q.CreateTopic(ctx, store.CreateTopicParams{
			ID: id, WorkspaceID: workspaceID, Name: in.Name, Color: in.Color,
			Priority: int16(in.Priority), TaskQueueID: in.TaskQueueID, Now: s.now(), //nolint:gosec // G115: checked to be 0..3
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTopicExists
		}
		if err != nil {
			return err
		}
		out, err = s.setTopicMembers(ctx, q, row, in.MemberIDs)
		return err
	})
	if err != nil {
		return Topic{}, topicError(err, "create topic")
	}
	return out, nil
}

// UpdateTopic changes a topic. Conversations of the topic keep the
// priority they have; new ones get the new one.
func (s *Service) UpdateTopic(ctx context.Context, workspaceID, id uuid.UUID, in TopicInput) (Topic, error) {
	in, err := in.valid()
	if err != nil {
		return Topic{}, err
	}
	var out Topic
	err = s.tx(ctx, workspaceID, func(q *store.Queries, tx pgx.Tx) error {
		row, err := q.UpdateTopic(ctx, store.UpdateTopicParams{
			Name: in.Name, Color: in.Color, Priority: int16(in.Priority), //nolint:gosec // G115: checked to be 0..3
			TaskQueueID: in.TaskQueueID, Now: s.now(), WorkspaceID: workspaceID, ID: id,
		})
		if err != nil {
			return err
		}
		if err := q.ClearTopicMembers(ctx, store.ClearTopicMembersParams{WorkspaceID: workspaceID, TopicID: id}); err != nil {
			return err
		}
		if out, err = s.setTopicMembers(ctx, q, row, in.MemberIDs); err != nil {
			return err
		}
		// Members changed: the queue may now go to someone.
		return s.queued(ctx, tx, workspaceID, nil)
	})
	if err != nil {
		return Topic{}, topicError(err, "update topic")
	}
	return out, nil
}

// DeleteTopic removes a topic: its conversations and tasks are left
// without one.
func (s *Service) DeleteTopic(ctx context.Context, workspaceID, id uuid.UUID) error {
	err := s.tx(ctx, workspaceID, func(q *store.Queries, tx pgx.Tx) error {
		n, err := q.DeleteTopic(ctx, store.DeleteTopicParams{WorkspaceID: workspaceID, ID: id})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return s.queued(ctx, tx, workspaceID, nil)
	})
	return wrap(err, "delete topic")
}

// ReorderTopics puts the topics in the order of ids, which must name each
// topic of the workspace once.
func (s *Service) ReorderTopics(ctx context.Context, workspaceID uuid.UUID, ids []uuid.UUID) ([]Topic, error) {
	var out []Topic
	err := s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		cur, err := listTopics(ctx, q, workspaceID)
		if err != nil {
			return err
		}
		have := make([]uuid.UUID, 0, len(cur))
		for _, t := range cur {
			have = append(have, t.ID)
		}
		byID := func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) }
		want := slices.Clone(ids)
		slices.SortFunc(have, byID)
		slices.SortFunc(want, byID)
		if !slices.Equal(have, want) {
			return fmt.Errorf("%w: ids must name every topic once", ErrInvalid)
		}
		for i, id := range ids {
			if _, err := q.SetTopicPosition(ctx, store.SetTopicPositionParams{
				Position: int32(i), WorkspaceID: workspaceID, ID: id, //nolint:gosec // G115: at most MaxTopics
			}); err != nil {
				return err
			}
		}
		out, err = listTopics(ctx, q, workspaceID)
		return err
	})
	if err != nil {
		return nil, wrap(err, "reorder topics")
	}
	return out, nil
}

// SetTopic sets the topic of a conversation, or removes it when topicID is
// nil, by an operator. Anyone who may tag works on any visible
// conversation: the topic classifies it, and the conversation stays with
// whoever has it.
func (s *Service) SetTopic(
	ctx context.Context, workspaceID uuid.UUID, actorID, conversationID uuid.UUID, topicID *uuid.UUID, version *int,
) (Conversation, error) {
	var out Conversation
	err := s.tx(ctx, workspaceID, func(q *store.Queries, tx pgx.Tx) error {
		row, err := q.LockConversation(ctx, store.LockConversationParams{WorkspaceID: workspaceID, ID: conversationID})
		if err != nil {
			return err
		}
		if version != nil && *version != int(row.Version) {
			return ErrConflict
		}
		topic, err := topicFor(ctx, q, workspaceID, topicID)
		if err != nil {
			return err
		}
		if err := s.applyTopic(ctx, q, tx, row, topic, actorID); err != nil {
			return err
		}
		out, err = getConversation(ctx, q, workspaceID, actorID, conversationID)
		return err
	})
	if err != nil {
		return Conversation{}, wrap(err, "set topic")
	}
	return out, nil
}

// ChooseThreadTopic records the topic the customer picked in a chat of a
// channel (a button of the Telegram bot) for its open conversation, and
// returns it. No open conversation, or no such topic, is ErrNotFound.
func (s *Service) ChooseThreadTopic(ctx context.Context, workspaceID, channelID uuid.UUID, thread string, topicID uuid.UUID) (Topic, error) {
	var out Topic
	err := s.tx(ctx, workspaceID, func(q *store.Queries, tx pgx.Tx) error {
		row, err := q.LockOpenThread(ctx, store.LockOpenThreadParams{
			WorkspaceID: workspaceID, ChannelID: &channelID, ExternalThreadID: &thread,
		})
		if err != nil {
			return err
		}
		t, err := q.GetTopic(ctx, store.GetTopicParams{WorkspaceID: workspaceID, ID: topicID})
		if err != nil {
			return err
		}
		out = topicFrom(t, nil)
		return s.customerTopic(ctx, q, tx, row, &t)
	})
	if err != nil {
		return Topic{}, wrap(err, "choose topic")
	}
	return out, nil
}

// ChooseVisitorTopic records the topic a widget visitor picked for an open
// conversation of their chat. With onlyIfNone a conversation that has a
// topic keeps it (a topic sent with a message). A conversation of anyone
// else, a settled one or no such topic is ErrNotFound.
func (s *Service) ChooseVisitorTopic(
	ctx context.Context, workspaceID uuid.UUID, chat VisitorChat, conversationID, topicID uuid.UUID, onlyIfNone bool,
) error {
	err := s.tx(ctx, workspaceID, func(q *store.Queries, tx pgx.Tx) error {
		row, err := q.LockVisitorConversation(ctx, store.LockVisitorConversationParams{
			WorkspaceID: workspaceID, ID: conversationID, ChannelID: &chat.ChannelID, Thread: &chat.Thread,
		})
		if err != nil {
			return err
		}
		if isSettled(Status(row.Status)) {
			return ErrNotFound
		}
		if onlyIfNone && row.TopicID != nil {
			return nil
		}
		t, err := q.GetTopic(ctx, store.GetTopicParams{WorkspaceID: workspaceID, ID: topicID})
		if err != nil {
			return err
		}
		return s.customerTopic(ctx, q, tx, row, &t)
	})
	return wrap(err, "choose visitor topic")
}

// customerTopic applies the topic the customer picked. A conversation the
// router gave to someone outside the topic's members, who has not replied
// yet, goes back to the queue for the members.
func (s *Service) customerTopic(ctx context.Context, q *store.Queries, tx pgx.Tx, row store.Conversation, t *store.Topic) error {
	if sameID(row.TopicID, &t.ID) {
		return nil
	}
	if err := s.applyTopic(ctx, q, tx, row, t, uuid.Nil); err != nil {
		return err
	}
	if Status(row.Status) != StatusAssigned || row.AssigneeID == nil || row.FirstResponseAt != nil {
		return nil
	}
	members, err := q.TopicMemberIDs(ctx, store.TopicMemberIDsParams{WorkspaceID: row.WorkspaceID, TopicID: t.ID})
	if err != nil || len(members) == 0 || slices.Contains(members, *row.AssigneeID) {
		return err
	}
	cur := state{Status: Status(row.Status), AssigneeID: row.AssigneeID, ResolvedAt: row.ResolvedAt, ClosedAt: row.ClosedAt}
	next, err := assign(cur, nil, s.now())
	if err != nil {
		return err
	}
	row.Version++ // applyTopic bumped it
	n, err := q.UpdateConversationState(ctx, store.UpdateConversationStateParams{
		WorkspaceID: row.WorkspaceID, ID: row.ID, Version: row.Version, Status: string(next.Status),
		AssigneeID: next.AssigneeID, ResolvedAt: next.ResolvedAt, ClosedAt: next.ClosedAt,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrConflict // cannot happen under the row lock
	}
	return s.recordChange(ctx, q, tx, row, uuid.Nil, cur, next)
}

// applyTopic sets the topic (nil: none) and its priority on a locked
// conversation, writes the feed line and the event. actorID is uuid.Nil
// when the customer picked it. A queued conversation is distributed again:
// its priority and who may take it changed.
func (s *Service) applyTopic(ctx context.Context, q *store.Queries, tx pgx.Tx, row store.Conversation, t *store.Topic, actorID uuid.UUID) error {
	var topicID *uuid.UUID
	priority := int16(0)
	if t != nil {
		topicID, priority = &t.ID, t.Priority
	}
	if sameID(row.TopicID, topicID) {
		return nil
	}
	if err := q.SetConversationTopic(ctx, store.SetConversationTopicParams{
		TopicID: topicID, Priority: priority, WorkspaceID: row.WorkspaceID, ID: row.ID,
	}); err != nil {
		return err
	}
	ev := Event{
		Type: EventTopicChanged, From: Status(row.Status), To: Status(row.Status),
		TopicID: topicID, PreviousTopicID: row.TopicID, ByCustomer: actorID == uuid.Nil,
	}
	if err := addEvent(ctx, q, tx, row, actorID, ev, s.now()); err != nil {
		return err
	}
	if err := outbox.Add(ctx, tx, row.WorkspaceID, "conversation.topic_changed", map[string]any{
		"conversation_id": row.ID, "topic_id": topicID,
	}); err != nil {
		return err
	}
	if Status(row.Status) == StatusQueued {
		return s.queued(ctx, tx, row.WorkspaceID, nil)
	}
	return nil
}

// TopicQuestion is what a channel asks a customer for: the topics in order
// and the workspace's language for the words around them.
type TopicQuestion struct {
	Topics []Topic
	Locale string
}

// TopicQuestion returns the topics a channel offers; no topics, nothing to
// ask.
func (s *Service) TopicQuestion(ctx context.Context, workspaceID uuid.UUID) (TopicQuestion, error) {
	var out TopicQuestion
	err := s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		var err error
		if out.Topics, err = listTopics(ctx, q, workspaceID); err != nil {
			return err
		}
		out.Locale, err = q.WorkspaceLocale(ctx, workspaceID)
		return err
	})
	if err != nil {
		return TopicQuestion{}, wrap(err, "topic question")
	}
	return out, nil
}

// askTopic decides, inside Receive, whether the channel asks the customer
// for the topic now: the conversation has none, was not asked yet, and the
// workspace has topics. Asking is recorded, so it happens once.
func (s *Service) askTopic(ctx context.Context, q *store.Queries, conv store.Conversation, now time.Time) (bool, error) {
	if conv.TopicID != nil || conv.TopicAskedAt != nil {
		return false, nil
	}
	n, err := q.CountTopics(ctx, conv.WorkspaceID)
	if err != nil || n == 0 {
		return false, err
	}
	return true, q.MarkTopicAsked(ctx, store.MarkTopicAskedParams{At: &now, WorkspaceID: conv.WorkspaceID, ID: conv.ID})
}

func (s *Service) setTopicMembers(ctx context.Context, q *store.Queries, row store.Topic, ids []uuid.UUID) (Topic, error) {
	if len(ids) > 0 {
		n, err := q.AddTopicMembers(ctx, store.AddTopicMembersParams{TopicID: row.ID, WorkspaceID: row.WorkspaceID, UserIds: ids})
		if err != nil {
			return Topic{}, err
		}
		if n != int64(len(ids)) {
			return Topic{}, fmt.Errorf("%w: a member is not an active member of the workspace", ErrInvalid)
		}
	}
	members, err := q.TopicMemberIDs(ctx, store.TopicMemberIDsParams{WorkspaceID: row.WorkspaceID, TopicID: row.ID})
	if err != nil {
		return Topic{}, err
	}
	slices.SortFunc(members, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	return topicFrom(row, members), nil
}

// topicFor reads the topic to set; nil for none.
func topicFor(ctx context.Context, q *store.Queries, workspaceID uuid.UUID, id *uuid.UUID) (*store.Topic, error) {
	if id == nil {
		return nil, nil
	}
	t, err := q.GetTopic(ctx, store.GetTopicParams{WorkspaceID: workspaceID, ID: *id})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: the topic does not exist", ErrInvalid)
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func listTopics(ctx context.Context, q *store.Queries, workspaceID uuid.UUID) ([]Topic, error) {
	rows, err := q.ListTopics(ctx, store.ListTopicsParams{WorkspaceID: workspaceID, Lim: MaxTopics})
	if err != nil {
		return nil, err
	}
	out := make([]Topic, 0, len(rows))
	for _, r := range rows {
		out = append(out, topicFrom(r.Topic, r.MemberIds))
	}
	return out, nil
}

// topicError turns a missing task queue (foreign key) into ErrInvalid and
// a name another topic has (a rename) into ErrTopicExists.
func topicError(err error, what string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return fmt.Errorf("%w: the task queue does not exist", ErrInvalid)
	}
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrTopicExists
	}
	return wrap(err, what)
}

func topicFrom(t store.Topic, members []uuid.UUID) Topic {
	if members == nil {
		members = []uuid.UUID{}
	}
	return Topic{
		ID: t.ID, Name: t.Name, Color: t.Color, Position: int(t.Position), Priority: int(t.Priority),
		TaskQueueID: t.TaskQueueID, MemberIDs: members, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
}
