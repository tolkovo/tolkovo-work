package notifications

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"

	"github.com/tolkovo/tolkovo/apps/server/internal/channels/telegram"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/notifications/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
)

// Telegram messages about conversations (docs/06 I-06), through the
// workspace's notifications bot (TK-02): a conversation was given to the
// member, the customer wrote in one of theirs, a customer's conversation
// waits in the queue. Only while the member's panel is closed: an open
// panel sounds and shows them itself. The message names the customer and
// the channel and links to the conversation; the text of the conversation
// never leaves for Telegram.

// What a message about a conversation tells.
const (
	ChatAssigned = "assigned"
	ChatMessage  = "message"
	ChatQueue    = "queue"
)

// ChatMessageEvery is how often a member hears about one conversation at
// most, for new customer messages and for the queue.
const ChatMessageEvery = 5 * time.Minute

// queueGrace lets the router assign a new conversation before the queue is
// told about it: a conversation given at once is news to its assignee only.
const queueGrace = 30 * time.Second

// answering are the roles that answer customers; others hear nothing about
// conversations (ADR-0020).
var answering = authz.RolesWith(authz.ConversationsReply)

func chatAbout(why conversations.Attention) string {
	if why == conversations.AttentionMessage {
		return ChatMessage
	}
	return ChatAssigned
}

// wantOf names the setting of a kind for ChatWatchers.
func wantOf(about string) string {
	switch about {
	case ChatAssigned:
		return "assigned"
	case ChatMessage:
		return "messages"
	default:
		return "queue"
	}
}

// WaitsInQueue queues a Telegram message to each member who may take the
// conversation; it implements conversations.Notifier.
func (s *Service) WaitsInQueue(ctx context.Context, tx pgx.Tx, workspaceID, conversationID uuid.UUID, topicID *uuid.UUID) error {
	return s.chatNotice(ctx, tx, workspaceID, conversationID, nil, topicID, ChatQueue)
}

// chatNotice queues one job per member who hears about it: userID alone,
// or everyone of the topic for the queue. The job checks the rest when it
// runs: the conversation moved on, the panel opened.
func (s *Service) chatNotice(
	ctx context.Context, tx pgx.Tx, workspaceID, conversationID uuid.UUID, userID, topicID *uuid.UUID, about string,
) error {
	if s.tg.Jobs == nil {
		return nil
	}
	q := store.New(tx)
	if _, err := q.GetNotifyBot(ctx, workspaceID); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	users := []uuid.UUID{}
	if userID != nil {
		users = append(users, *userID)
	}
	topic := pgtype.UUID{}
	if topicID != nil {
		topic = pgtype.UUID{Bytes: *topicID, Valid: true}
	}
	watchers, err := q.ChatWatchers(ctx, store.ChatWatchersParams{
		WorkspaceID: workspaceID, Answering: answering, UserIds: users, Want: wantOf(about), TopicID: topic,
	})
	if err != nil {
		return fmt.Errorf("chat watchers: %w", err)
	}
	for _, w := range watchers {
		opts := &river.InsertOpts{}
		switch about {
		case ChatMessage:
			opts.UniqueOpts = river.UniqueOpts{ByArgs: true, ByPeriod: ChatMessageEvery}
		case ChatQueue:
			opts.ScheduledAt = s.now().Add(queueGrace)
			opts.UniqueOpts = river.UniqueOpts{ByArgs: true, ByPeriod: ChatMessageEvery}
		}
		if _, err := s.tg.Jobs.InsertTx(ctx, tx, ChatMessageArgs{
			WorkspaceID: workspaceID, ConversationID: conversationID, UserID: w.UserID, About: about,
		}, opts); err != nil {
			return fmt.Errorf("queue telegram message: %w", err)
		}
	}
	return nil
}

// ChatMessageArgs sends one message about a conversation to one member.
// Only ids: the job reads the conversation, the chat and the names when it
// runs.
type ChatMessageArgs struct {
	WorkspaceID    uuid.UUID `json:"workspace_id"`
	ConversationID uuid.UUID `json:"conversation_id"`
	UserID         uuid.UUID `json:"user_id"`
	// About is ChatAssigned, ChatMessage or ChatQueue.
	About string `json:"about"`
}

// Kind implements river.JobArgs.
func (ChatMessageArgs) Kind() string { return "notifications.telegram_chat" }

// InsertOpts implements river.JobArgsWithInsertOpts, as TaskMessageArgs.
func (ChatMessageArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 8} }

// Conversations reads what a message about a conversation needs;
// *conversations.Service implements it.
type Conversations interface {
	Conversation(ctx context.Context, workspaceID, viewerID, id uuid.UUID) (conversations.Conversation, error)
}

// Panels tells whose panel is open in a workspace: they hear about
// conversations there, not in Telegram.
type Panels func(ctx context.Context, workspaceID uuid.UUID) (map[uuid.UUID]bool, error)

type chatMessageWorker struct {
	river.WorkerDefaults[ChatMessageArgs]
	opts TelegramWorkerOptions
}

// Timeout bounds one message.
func (w *chatMessageWorker) Timeout(*river.Job[ChatMessageArgs]) time.Duration {
	return 30 * time.Second
}

// Work implements river.Worker.
func (w *chatMessageWorker) Work(ctx context.Context, job *river.Job[ChatMessageArgs]) error {
	return SendChatMessage(ctx, w.opts, job.Args)
}

// SendChatMessage sends one message about a conversation (the job's work).
// It ends quietly when the message is no longer news: the conversation
// went to someone else or out of the queue, the customer's messages were
// read, the member opened the panel, turned the kind off or unlinked
// Telegram, the bot is gone.
func SendChatMessage(ctx context.Context, o TelegramWorkerOptions, a ChatMessageArgs) error {
	s := o.Service
	_, token, err := s.openBot(ctx, a.WorkspaceID)
	if errors.Is(err, ErrNoBot) {
		return nil
	}
	if err != nil {
		return err
	}
	c, err := o.Conversations.Conversation(ctx, a.WorkspaceID, a.UserID, a.ConversationID)
	if errors.Is(err, conversations.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !stillNews(c, a) {
		return nil
	}
	open, err := o.Panels(ctx, a.WorkspaceID)
	if err != nil {
		return err
	}
	if open[a.UserID] {
		return nil
	}
	var (
		chat    int64
		channel string
	)
	err = s.db.InTenantTx(ctx, a.WorkspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		rows, err := q.ChatWatchers(ctx, store.ChatWatchersParams{
			WorkspaceID: a.WorkspaceID, Answering: answering, UserIds: []uuid.UUID{a.UserID}, Want: wantOf(a.About),
		})
		if err != nil || len(rows) != 1 {
			return err
		}
		chat = rows[0].ChatID
		if c.ChannelID == nil {
			return nil
		}
		ch, err := q.ChannelName(ctx, store.ChannelNameParams{WorkspaceID: a.WorkspaceID, ID: *c.ChannelID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		channel = ch.Name
		return err
	})
	if err != nil || chat == 0 {
		return err
	}
	locale := "ru"
	if o.Locale != nil {
		if l, err := o.Locale(ctx, a.WorkspaceID, a.UserID); err == nil && l == "en" {
			locale = l
		}
	}
	text := chatMessage(locale, a.About, c, channel, strings.TrimRight(o.PanelURL, "/"))
	_, err = s.tg.Bots.SendNotice(ctx, token.Reveal(), chat, text)
	var (
		refused *telegram.UndeliverableError
		apiErr  *telegram.APIError
	)
	if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
		return river.JobSnooze(apiErr.RetryAfter)
	}
	if errors.As(err, &refused) {
		if refused.Reason == telegram.ReasonBlocked || refused.Reason == telegram.ReasonChatNotFound {
			return s.db.InTenantTx(ctx, a.WorkspaceID, func(tx pgx.Tx) error {
				return store.New(tx).UnlinkChat(ctx, store.UnlinkChatParams{WorkspaceID: a.WorkspaceID, UserID: a.UserID, ChatID: pgtype.Int8{Int64: chat, Valid: true}})
			})
		}
		return nil // a retry gets the same answer
	}
	return err
}

// stillNews reports whether the conversation still is what the message
// tells.
func stillNews(c conversations.Conversation, a ChatMessageArgs) bool {
	mine := c.AssigneeID != nil && *c.AssigneeID == a.UserID &&
		slices.Contains([]conversations.Status{conversations.StatusAssigned, conversations.StatusPending}, c.Status)
	switch a.About {
	case ChatAssigned:
		return mine
	case ChatMessage:
		return mine && c.UnreadCount > 0
	default:
		return c.Status == conversations.StatusQueued
	}
}

// chatMessage is the text of a message: what happened, the customer's name,
// the channel and the link, nothing of what was written.
func chatMessage(locale, about string, c conversations.Conversation, channel, panel string) string {
	var head string
	switch {
	case locale == "en" && about == ChatAssigned:
		head = fmt.Sprintf("Conversation №%d is yours now", c.Number)
	case locale == "en" && about == ChatMessage:
		head = fmt.Sprintf("New message in conversation №%d", c.Number)
	case locale == "en":
		head = fmt.Sprintf("New conversation in the queue: №%d", c.Number)
	case about == ChatAssigned:
		head = fmt.Sprintf("Вам передан диалог №%d", c.Number)
	case about == ChatMessage:
		head = fmt.Sprintf("Новое сообщение в диалоге №%d", c.Number)
	default:
		head = fmt.Sprintf("Новый диалог в очереди: №%d", c.Number)
	}
	who := c.ContactName
	if channel != "" {
		who += " · " + channel
	}
	text := head + "\n" + who
	if panel != "" {
		text += "\n" + panel + "/inbox/" + c.ID.String()
	}
	return text
}
