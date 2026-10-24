package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/tolkovo/tolkovo/apps/server/internal/ai/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
)

// The AI bot of the first line (ADR-0018, AI-04). A new conversation of a
// channel the bot is on starts in status bot (conversations.Bot.Answers);
// every customer message queues ai.reply, which waits for a pause in the
// customer's typing, finds a few entries of the knowledge base, asks the
// model once and checks its answer before the customer sees it. The bot
// replies, collects the details of a bug for a task of the IT team, or
// hands the conversation to people with a note of what it learnt.

// Limits of the bot.
const (
	// MaxBotReplies is how often the bot replies in one conversation
	// before it hands over.
	MaxBotReplies = 10
	// MaxCollectTurns is how many turns the bot asks for the details of a
	// bug before it hands over with what it has.
	MaxCollectTurns = 4
	// MaxHandoffMessage bounds BotSettings.HandoffMessage.
	MaxHandoffMessage = 500
	// MaxBotName bounds BotSettings.Name.
	MaxBotName = 40
	// MaxBotRules bounds BotSettings.Rules.
	MaxBotRules = 2000
	// MaxBotChannels bounds BotSettings.ChannelIDs.
	MaxBotChannels = 100
	// DefaultMinConfidence is how sure the model must be of an answer from
	// the knowledge base, in percent, until the admin changes it: strict.
	DefaultMinConfidence = 75
	// botPause is how long the customer has been quiet before the bot
	// answers: several messages in a row get one answer.
	botPause = 2 * time.Second
	// botBusyWait is when a turn comes back while another one asks the model.
	botBusyWait = 3 * time.Second
	// botHistory is how many messages of the conversation the model reads.
	botHistory = 20
	// botAttempts bounds the runs of a failing turn: each may pay for a
	// call of the model.
	botAttempts = 3
)

// Tones of the bot.
const (
	ToneFriendly = "friendly"
	ToneNeutral  = "neutral"
	ToneFormal   = "formal"
)

// Forms of address in languages that have them: Russian «вы» and «ты».
const (
	AddressFormal   = "formal"
	AddressInformal = "informal"
)

// BotSettings are the settings of the bot (AI-04, AI-05).
type BotSettings struct {
	// ChannelIDs: the bot answers new conversations of these channels.
	ChannelIDs []uuid.UUID
	// TopicsOff: conversations of these topics go to people at once.
	TopicsOff []uuid.UUID
	// MinConfidence: an answer from the knowledge base the model is less
	// sure of, in percent, goes to people instead.
	MinConfidence int
	// HandoffMessage is what the customer reads when the bot hands over
	// without words of its own; empty: the built-in phrase.
	HandoffMessage string
	// Name is what the bot introduces itself by; empty: "the AI assistant".
	Name string
	// Tone is ToneFriendly, ToneNeutral or ToneFormal.
	Tone string
	// Address is AddressFormal or AddressInformal.
	Address string
	// Rules are the company's own instructions to the bot; they never
	// override the prompt's rules on honesty and handoff.
	Rules string
}

// BotSettings returns the settings of the bot.
func (s *Service) BotSettings(ctx context.Context, workspaceID uuid.UUID) (BotSettings, error) {
	var out BotSettings
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		out, err = botSettings(ctx, store.New(tx), workspaceID)
		return err
	})
	if err != nil {
		return BotSettings{}, fmt.Errorf("bot settings: %w", err)
	}
	return out, nil
}

// SetBotSettings saves the settings of the bot. A channel or a topic that
// is not the workspace's is ErrInvalid.
func (s *Service) SetBotSettings(ctx context.Context, workspaceID uuid.UUID, in BotSettings) (BotSettings, error) {
	in.HandoffMessage, in.Name, in.Rules = strings.TrimSpace(in.HandoffMessage), oneLine(in.Name), strings.TrimSpace(in.Rules)
	if in.Tone == "" {
		in.Tone = ToneFriendly
	}
	if in.Address == "" {
		in.Address = AddressFormal
	}
	switch {
	case in.MinConfidence < 0 || in.MinConfidence > 100:
		return BotSettings{}, fmt.Errorf("%w: the confidence is 0 to 100", ErrInvalid)
	case utf8.RuneCountInString(in.HandoffMessage) > MaxHandoffMessage || !validText(in.HandoffMessage):
		return BotSettings{}, fmt.Errorf("%w: the handoff message is at most %d characters", ErrInvalid, MaxHandoffMessage)
	case utf8.RuneCountInString(in.Name) > MaxBotName || !validText(in.Name):
		return BotSettings{}, fmt.Errorf("%w: the name is at most %d characters", ErrInvalid, MaxBotName)
	case utf8.RuneCountInString(in.Rules) > MaxBotRules || !validText(in.Rules):
		return BotSettings{}, fmt.Errorf("%w: the rules are at most %d characters", ErrInvalid, MaxBotRules)
	case in.Tone != ToneFriendly && in.Tone != ToneNeutral && in.Tone != ToneFormal:
		return BotSettings{}, fmt.Errorf("%w: unknown tone %q", ErrInvalid, in.Tone)
	case in.Address != AddressFormal && in.Address != AddressInformal:
		return BotSettings{}, fmt.Errorf("%w: unknown form of address %q", ErrInvalid, in.Address)
	case len(in.ChannelIDs) > MaxBotChannels || len(in.TopicsOff) > conversations.MaxTopics:
		return BotSettings{}, fmt.Errorf("%w: too many channels or topics", ErrInvalid)
	}
	channels, topics := uniqueIDs(in.ChannelIDs), uniqueIDs(in.TopicsOff)
	var out BotSettings
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.SetBotOptions(ctx, store.SetBotOptionsParams{
			WorkspaceID: workspaceID, MinConfidence: int16(in.MinConfidence), HandoffMessage: in.HandoffMessage, //nolint:gosec // G115: checked to be 0..100
			BotName: in.Name, Tone: in.Tone, Address: in.Address, Rules: in.Rules, Now: s.now(),
		}); err != nil {
			return err
		}
		if err := q.ClearBotChannels(ctx, workspaceID); err != nil {
			return err
		}
		n, err := q.AddBotChannels(ctx, store.AddBotChannelsParams{WorkspaceID: workspaceID, ChannelIds: channels})
		if err != nil {
			return err
		}
		if n != int64(len(channels)) {
			return fmt.Errorf("%w: a channel does not exist", ErrInvalid)
		}
		if err := q.ClearBotTopicsOff(ctx, workspaceID); err != nil {
			return err
		}
		if n, err = q.AddBotTopicsOff(ctx, store.AddBotTopicsOffParams{WorkspaceID: workspaceID, TopicIds: topics}); err != nil {
			return err
		}
		if n != int64(len(topics)) {
			return fmt.Errorf("%w: a topic does not exist", ErrInvalid)
		}
		out, err = botSettings(ctx, q, workspaceID)
		return err
	})
	if err != nil {
		if errors.Is(err, ErrInvalid) {
			return BotSettings{}, err
		}
		return BotSettings{}, fmt.Errorf("set bot settings: %w", err)
	}
	return out, nil
}

func botSettings(ctx context.Context, q *store.Queries, workspaceID uuid.UUID) (BotSettings, error) {
	row, err := q.GetBotSettings(ctx, workspaceID)
	if err != nil {
		return BotSettings{}, err
	}
	return BotSettings{
		ChannelIDs: row.ChannelIds, TopicsOff: row.TopicsOff, MinConfidence: int(row.MinConfidence), HandoffMessage: row.HandoffMessage,
		Name: row.BotName, Tone: row.Tone, Address: row.Address, Rules: row.Rules,
	}, nil
}

func uniqueIDs(ids []uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// Jobs queues River jobs in a transaction; *jobs.Client implements it.
type Jobs interface {
	InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// SetJobs connects the job queue the bot's turns go to; call it before
// serving. Without it Wake queues nothing.
func (s *Service) SetJobs(j Jobs) { s.jobs = j }

// Answers implements conversations.Bot: the bot is on for the channel and
// AI is on in the workspace.
func (s *Service) Answers(ctx context.Context, tx pgx.Tx, workspaceID, channelID uuid.UUID) (bool, error) {
	if s.jobs == nil {
		return false, nil // nobody would run its turns
	}
	return store.New(tx).BotAnswersChannel(ctx, store.BotAnswersChannelParams{WorkspaceID: workspaceID, ChannelID: channelID})
}

// Wake implements conversations.Bot: it queues the bot's turn after the
// pause. Every message queues its own job, without uniqueness: the first
// to run takes all the messages so far, the others find nothing to do.
func (s *Service) Wake(ctx context.Context, tx pgx.Tx, workspaceID, conversationID uuid.UUID) error {
	if s.jobs == nil {
		return nil
	}
	_, err := s.jobs.InsertTx(ctx, tx, ReplyArgs{WorkspaceID: workspaceID, ConversationID: conversationID},
		&river.InsertOpts{ScheduledAt: s.now().Add(botPause), MaxAttempts: botAttempts})
	if err != nil {
		return fmt.Errorf("queue bot turn: %w", err)
	}
	return nil
}

// ReplyArgs is the job of one turn of the bot.
type ReplyArgs struct {
	WorkspaceID    uuid.UUID `json:"workspace_id"`
	ConversationID uuid.UUID `json:"conversation_id"`
}

// Kind implements river.JobArgs.
func (ReplyArgs) Kind() string { return "ai.reply" }

// BotInbox is the part of the conversations module the bot works through;
// *conversations.Service implements it.
type BotInbox interface {
	BotView(ctx context.Context, workspaceID, conversationID uuid.UUID, limit int) (conversations.BotView, error)
	LockBotConversation(ctx context.Context, tx pgx.Tx, workspaceID, conversationID uuid.UUID) (bool, error)
	ApplyBotTurn(ctx context.Context, tx pgx.Tx, workspaceID, conversationID uuid.UUID, t conversations.BotTurn) (bool, error)
}

// BotTasks creates the tasks the bot collects; *tasks.Service implements
// it. The number is 0, without an error, while the tasks section is off.
type BotTasks interface {
	CreateFromBot(ctx context.Context, tx pgx.Tx, workspaceID, conversationID uuid.UUID, title, body string) (int64, error)
}

type replyWorker struct {
	river.WorkerDefaults[ReplyArgs]
	svc   *Service
	inbox BotInbox
	tasks BotTasks
}

// Work implements river.Worker.
func (w *replyWorker) Work(ctx context.Context, job *river.Job[ReplyArgs]) error {
	wait, err := w.svc.BotTurn(ctx, w.inbox, w.tasks, job.ID, job.Args)
	if err == nil && wait > 0 {
		return river.JobSnooze(wait)
	}
	return err
}

// Timeout bounds a turn: one call of the model and its retry, and the rows.
func (w *replyWorker) Timeout(*river.Job[ReplyArgs]) time.Duration {
	return w.svc.opts.Timeout + 30*time.Second
}

// AddBotWorker registers the bot's job; inbox is the conversations module
// and tasks the tasks module (nil: the bot hands bugs over untasked).
func (s *Service) AddBotWorker(workers *river.Workers, inbox BotInbox, tasks BotTasks) {
	river.AddWorker(workers, &replyWorker{svc: s, inbox: inbox, tasks: tasks})
}

// claim is the outcome of claiming a turn.
type claim struct {
	state store.AiBotConversation
	// wait > 0: come back then (the customer is still typing, or another
	// turn asks the model); done: nothing new to answer.
	wait time.Duration
	done bool
}

// claimTurn takes the conversation's turn for job jobID up to the customer
// message last, unless a turn took it already or another one holds it.
func (s *Service) claimTurn(ctx context.Context, workspaceID, conversationID uuid.UUID, jobID int64, last conversations.BotLine) (claim, error) {
	var out claim
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		now := s.now()
		if err := q.EnsureBotConversation(ctx, store.EnsureBotConversationParams{WorkspaceID: workspaceID, ConversationID: conversationID, Now: now}); err != nil {
			return err
		}
		st, err := q.LockBotConversation(ctx, store.LockBotConversationParams{WorkspaceID: workspaceID, ConversationID: conversationID})
		if err != nil {
			return err
		}
		out.state = st
		switch {
		case st.AnsweredUpTo != nil && !after(last.ID, *st.AnsweredUpTo):
			out.done = true
			return nil
		case st.ClaimedBy != nil && *st.ClaimedBy != jobID && st.ClaimedAt.Valid && now.Sub(st.ClaimedAt.Time) < s.claimTTL():
			out.wait = botBusyWait
			return nil
		case now.Sub(last.At) < botPause:
			out.wait = botPause - now.Sub(last.At)
			return nil
		}
		return q.ClaimBotTurn(ctx, store.ClaimBotTurnParams{JobID: &jobID, Now: now, WorkspaceID: workspaceID, ConversationID: conversationID})
	})
	return out, err
}

// claimTTL is when a claim of a stopped worker lapses: past a call's
// timeout with its retry.
func (s *Service) claimTTL() time.Duration { return 2*s.opts.Timeout + time.Minute }

// releaseTurn gives a claim back without a turn.
func (s *Service) releaseTurn(ctx context.Context, workspaceID, conversationID uuid.UUID, jobID int64) error {
	return s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		return store.New(tx).ReleaseBotTurn(ctx, store.ReleaseBotTurnParams{Now: s.now(), WorkspaceID: workspaceID, ConversationID: conversationID, JobID: &jobID})
	})
}

// after reports whether UUIDv7 a was made after b.
func after(a, b uuid.UUID) bool { return bytes.Compare(a[:], b[:]) > 0 }

// BotTurn runs one turn of the bot for job jobID. A positive wait asks to
// run again after it.
func (s *Service) BotTurn(ctx context.Context, inbox BotInbox, tasks BotTasks, jobID int64, args ReplyArgs) (time.Duration, error) {
	ws, convID := args.WorkspaceID, args.ConversationID
	view, err := inbox.BotView(ctx, ws, convID, botHistory)
	if errors.Is(err, conversations.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if view.Status != conversations.StatusBot {
		return 0, nil
	}
	var last *conversations.BotLine
	for k := len(view.Lines) - 1; k >= 0; k-- {
		if view.Lines[k].From == conversations.SenderContact {
			last = &view.Lines[k]
			break
		}
	}
	if last == nil {
		return 0, nil
	}
	c, err := s.claimTurn(ctx, ws, convID, jobID, *last)
	if err != nil || c.done || c.wait > 0 {
		return c.wait, err
	}
	p, err := s.plan(ctx, ws, convID, view, c.state)
	if err != nil {
		return 0, errors.Join(err, s.releaseTurn(context.WithoutCancel(ctx), ws, convID, jobID))
	}
	err = s.db.InTenantTx(ctx, ws, func(tx pgx.Tx) error {
		return s.apply(ctx, tx, inbox, tasks, jobID, ws, convID, last.ID, c.state, p)
	})
	if err != nil {
		return 0, errors.Join(fmt.Errorf("bot turn: %w", err), s.releaseTurn(context.WithoutCancel(ctx), ws, convID, jobID))
	}
	return 0, nil
}

// apply records the turn: the task, the reply and the handoff, and the
// bot's state, unless the claim lapsed and another turn took over.
func (s *Service) apply(
	ctx context.Context, tx pgx.Tx, inbox BotInbox, tasks BotTasks, jobID int64,
	ws, convID, upTo uuid.UUID, prev store.AiBotConversation, p plan,
) error {
	q := store.New(tx)
	st, err := q.LockBotConversation(ctx, store.LockBotConversationParams{WorkspaceID: ws, ConversationID: convID})
	if err != nil {
		return err
	}
	if st.ClaimedBy == nil || *st.ClaimedBy != jobID {
		return nil // lapsed: another turn answers
	}
	now := s.now()
	if p.waitTopic {
		return q.ReleaseBotTurn(ctx, store.ReleaseBotTurnParams{Now: now, WorkspaceID: ws, ConversationID: convID, JobID: &jobID})
	}
	finish := func(replied bool) error {
		replies := prev.Replies
		if replied {
			replies++
		}
		collected, err := json.Marshal(p.collected)
		if err != nil {
			return err
		}
		return q.FinishBotTurn(ctx, store.FinishBotTurnParams{
			AnsweredUpTo: &upTo, Replies: replies, CollectTurns: int32(p.collectTurns), Collected: collected, //nolint:gosec // G115: at most MaxCollectTurns
			Now: now, WorkspaceID: ws, ConversationID: convID,
		})
	}
	ok, err := inbox.LockBotConversation(ctx, tx, ws, convID)
	if err != nil {
		return err
	}
	if !ok {
		return finish(false) // an operator took it meanwhile
	}
	turn := p.turn
	if p.task != nil {
		var n int64
		if tasks != nil {
			if n, err = tasks.CreateFromBot(ctx, tx, ws, convID, p.task.title, p.task.body); err != nil {
				return err
			}
		}
		if n > 0 {
			turn.Reply = strings.TrimSpace(turn.Reply + "\n\n" + fmt.Sprintf(phrase(p.locale, phraseTask), n))
			turn.HandOff, turn.Reason = true, conversations.HandoffTask
		} else {
			// No tasks: people take the details from the note.
			turn.Reply, turn.HandOff, turn.Reason = p.fallback, true, conversations.HandoffIncomplete
		}
	}
	if _, err := inbox.ApplyBotTurn(ctx, tx, ws, convID, turn); err != nil {
		return err
	}
	return finish(turn.Reply != "")
}
