package conversations

import (
	"context"
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

// The AI bot of the first line (ADR-0018). A new conversation of a channel
// the bot answers starts in status bot, out of the queue; each customer
// message wakes the bot, which replies or hands the conversation to people
// (bot → queued). An operator may take it from the bot at any time.

// Bot is the AI module's side of it. Both methods run inside the
// transaction of the change: they may write rows and queue jobs there,
// never call the network.
type Bot interface {
	// Answers reports whether the bot takes a new conversation of the channel.
	Answers(ctx context.Context, tx pgx.Tx, workspaceID, channelID uuid.UUID) (bool, error)
	// Wake queues the bot's turn in a conversation it answers: the customer
	// wrote, or picked the topic.
	Wake(ctx context.Context, tx pgx.Tx, workspaceID, conversationID uuid.UUID) error
}

// SetBot connects the AI bot; call it before serving. Without it every
// conversation goes to the queue.
func (s *Service) SetBot(b Bot) { s.bot = b }

// Why the bot handed a conversation to people (Event.Reason of
// EventBotHandoff).
const (
	// HandoffAsked: the customer asked for a person.
	HandoffAsked = "customer_asked"
	// HandoffTopic: the conversation's topic is left to people.
	HandoffTopic = "topic"
	// HandoffUnknown: the bot did not know the answer or was not sure.
	HandoffUnknown = "unknown"
	// HandoffRules: the bot's rules send such questions to people.
	HandoffRules = "rules"
	// HandoffChecks: the answer failed the checks and was not sent.
	HandoffChecks = "checks_failed"
	// HandoffUnavailable: AI is off, its budget is spent or the model failed.
	HandoffUnavailable = "ai_unavailable"
	// HandoffLimit: the bot has replied as often as it may in one conversation.
	HandoffLimit = "limit"
	// HandoffTask: the bot created a task for the IT team.
	HandoffTask = "task_created"
	// HandoffIncomplete: the bot collected what it could for a task of the
	// IT team but could not create it (details missing, tasks off).
	HandoffIncomplete = "task_incomplete"
)

var handoffReasons = []string{
	HandoffAsked, HandoffTopic, HandoffUnknown, HandoffRules, HandoffChecks, HandoffUnavailable, HandoffLimit, HandoffTask, HandoffIncomplete,
}

// MaxBotLines bounds BotView.Lines.
const MaxBotLines = 50

// BotView is what the bot reads of a conversation.
type BotView struct {
	Status    Status
	ChannelID *uuid.UUID
	TopicID   *uuid.UUID
	// TopicAsked: the channel asked the customer for the topic.
	TopicAsked bool
	// Locale is the workspace's language, ru or en.
	Locale string
	// Lines are what the customer, the bot and operators wrote to each
	// other, oldest first.
	Lines []BotLine
}

// BotLine is one message of BotView.
type BotLine struct {
	ID uuid.UUID
	// From is SenderContact, SenderBot or SenderOperator.
	From  string
	Text  string
	Files int
	At    time.Time
}

// BotView returns the conversation as the bot reads it, with its last
// limit messages (at most MaxBotLines).
func (s *Service) BotView(ctx context.Context, workspaceID, conversationID uuid.UUID, limit int) (BotView, error) {
	limit = min(max(limit, 1), MaxBotLines)
	var out BotView
	err := s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		row, err := q.GetConversation(ctx, store.GetConversationParams{WorkspaceID: workspaceID, ID: conversationID})
		if err != nil {
			return err
		}
		c := row.Conversation
		out = BotView{Status: Status(c.Status), ChannelID: c.ChannelID, TopicID: c.TopicID, TopicAsked: c.TopicAskedAt != nil}
		if out.Locale, err = q.WorkspaceLocale(ctx, workspaceID); err != nil {
			return err
		}
		lines, err := q.BotLines(ctx, store.BotLinesParams{WorkspaceID: workspaceID, ConversationID: conversationID, Lim: int32(limit)}) //nolint:gosec // G115: at most MaxBotLines
		if err != nil {
			return err
		}
		out.Lines = make([]BotLine, 0, len(lines))
		for _, l := range slices.Backward(lines) {
			out.Lines = append(out.Lines, BotLine{ID: l.ID, From: l.SenderType, Text: l.Body, Files: int(l.AttachmentsCount), At: l.CreatedAt})
		}
		return nil
	})
	if err != nil {
		return BotView{}, wrap(err, "bot view")
	}
	return out, nil
}

// BotTurn is what the bot does in one turn.
type BotTurn struct {
	// Reply is what the customer reads; empty: nothing.
	Reply string
	// HandOff gives the conversation to people, for Reason (Handoff…).
	HandOff bool
	Reason  string
	// Note is left for operators (what the customer wants, what the bot
	// collected); empty: none.
	Note string
}

// LockBotConversation locks the conversation in the caller's transaction
// and reports whether the bot still answers it: an operator may have taken
// it meanwhile.
func (s *Service) LockBotConversation(ctx context.Context, tx pgx.Tx, workspaceID, conversationID uuid.UUID) (bool, error) {
	row, err := store.New(tx).LockConversation(ctx, store.LockConversationParams{WorkspaceID: workspaceID, ID: conversationID})
	if err != nil {
		return false, wrap(err, "lock bot conversation")
	}
	return Status(row.Status) == StatusBot, nil
}

// ApplyBotTurn records a turn of the bot in the caller's transaction: its
// reply, handed to the channel, its note, and the handoff to the queue. A
// conversation the bot no longer answers is left alone (false).
func (s *Service) ApplyBotTurn(ctx context.Context, tx pgx.Tx, workspaceID, conversationID uuid.UUID, t BotTurn) (bool, error) {
	t.Reply, t.Note = strings.TrimSpace(t.Reply), strings.TrimSpace(t.Note)
	switch {
	case utf8.RuneCountInString(t.Reply) > MaxBodyLength || utf8.RuneCountInString(t.Note) > MaxBodyLength:
		return false, fmt.Errorf("%w: a bot message is at most %d characters", ErrInvalid, MaxBodyLength)
	case t.HandOff && !slices.Contains(handoffReasons, t.Reason):
		return false, fmt.Errorf("%w: unknown handoff reason %q", ErrInvalid, t.Reason)
	case !t.HandOff && t.Reply == "":
		return false, fmt.Errorf("%w: a bot turn replies or hands over", ErrInvalid)
	}
	q := store.New(tx)
	row, err := q.LockConversation(ctx, store.LockConversationParams{WorkspaceID: workspaceID, ID: conversationID})
	if err != nil {
		return false, wrap(err, "bot turn")
	}
	if Status(row.Status) != StatusBot {
		return false, nil
	}
	now := s.now()
	if t.Reply != "" {
		if err := s.botMessage(ctx, q, tx, row, t.Reply, false, now); err != nil {
			return false, wrap(err, "bot reply")
		}
		row.Version++ // TouchConversation
	}
	if t.Note != "" {
		if err := s.botMessage(ctx, q, tx, row, t.Note, true, now); err != nil {
			return false, wrap(err, "bot note")
		}
	}
	if !t.HandOff {
		return true, nil
	}
	cur := state{Status: StatusBot}
	next, err := transition(cur, StatusQueued, now)
	if err != nil {
		return false, err
	}
	n, err := q.UpdateConversationState(ctx, store.UpdateConversationStateParams{
		WorkspaceID: workspaceID, ID: conversationID, Version: row.Version, Status: string(next.Status),
		AssigneeID: next.AssigneeID, ResolvedAt: next.ResolvedAt, ClosedAt: next.ClosedAt,
	})
	if err != nil {
		return false, wrap(err, "bot handoff")
	}
	if n == 0 {
		return false, ErrConflict // the row is locked: cannot happen
	}
	if err := s.addStateEvents(ctx, tx, row, uuid.Nil, cur, next); err != nil {
		return false, err
	}
	ev := Event{Type: EventBotHandoff, From: cur.Status, To: next.Status, Reason: t.Reason}
	return true, addEvent(ctx, q, tx, row, uuid.Nil, ev, now)
}

// botMessage stores a reply of the bot, queued for the channel, or its
// note for operators.
func (s *Service) botMessage(ctx context.Context, q *store.Queries, tx pgx.Tx, conv store.Conversation, body string, note bool, now time.Time) error {
	id, err := newID()
	if err != nil {
		return err
	}
	kind := KindText
	var delivery *string
	switch {
	case note:
		kind = KindNote
	case conv.ChannelID != nil:
		delivery = ptr(DeliveryPending)
	}
	if _, err := q.CreateBotMessage(ctx, store.CreateBotMessageParams{
		ID: id, WorkspaceID: conv.WorkspaceID, ConversationID: conv.ID, Kind: kind, Body: body,
		IsPrivate: note, DeliveryStatus: delivery, CreatedAt: now,
	}); err != nil {
		return err
	}
	if !note {
		if err := q.TouchConversation(ctx, store.TouchConversationParams{WorkspaceID: conv.WorkspaceID, ID: conv.ID, At: now}); err != nil {
			return err
		}
	}
	if err := outbox.Add(ctx, tx, conv.WorkspaceID, "message.created", map[string]any{
		"conversation_id": conv.ID, "message_id": id, "kind": kind, "is_private": note,
		"sender_type": SenderBot, "sender_id": nil,
	}); err != nil {
		return err
	}
	if delivery != nil {
		return s.queueDelivery(ctx, tx, conv, id)
	}
	return nil
}

// takeFromBot gives a locked conversation of the bot to the member who
// replies in it (bot → assigned).
func (s *Service) takeFromBot(ctx context.Context, q *store.Queries, tx pgx.Tx, conv store.Conversation, userID uuid.UUID) error {
	cur := state{Status: StatusBot}
	next, err := assign(cur, &userID, s.now())
	if err != nil {
		return err
	}
	n, err := q.UpdateConversationState(ctx, store.UpdateConversationStateParams{
		WorkspaceID: conv.WorkspaceID, ID: conv.ID, Version: conv.Version, Status: string(next.Status),
		AssigneeID: next.AssigneeID, ResolvedAt: next.ResolvedAt, ClosedAt: next.ClosedAt,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrConflict // the row is locked: cannot happen
	}
	return s.recordChange(ctx, q, tx, conv, userID, cur, next)
}

// wakeBot wakes the bot in a conversation it answers.
func (s *Service) wakeBot(ctx context.Context, tx pgx.Tx, conv store.Conversation) error {
	if s.bot == nil || Status(conv.Status) != StatusBot {
		return nil
	}
	if err := s.bot.Wake(ctx, tx, conv.WorkspaceID, conv.ID); err != nil {
		return fmt.Errorf("wake bot: %w", err)
	}
	return nil
}
