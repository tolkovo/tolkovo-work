package notifications

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/tolkovo/tolkovo/apps/server/internal/channels/telegram"
	"github.com/tolkovo/tolkovo/apps/server/internal/notifications/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/crypto"
	"github.com/tolkovo/tolkovo/apps/server/internal/tasks"
)

// Telegram notifications of members (TK-02, ADR-0015). A workspace adds a
// bot of its own just for notifications; a member links their Telegram
// with t.me/<bot>?start=<code>, and the server finds the code in the bot's
// updates (getUpdates, no webhook: it works behind NAT). Messages about
// tasks are queued in the transaction of the change and sent by a job; the
// job reads the task when it sends, so the queue holds ids only.

// LinkTTL is how long a link code waits for Start.
const LinkTTL = 15 * time.Minute

// CheckInterval is how often one workspace's bot is read at most: panels
// waiting for Start ask every few seconds, one read serves them all.
const CheckInterval = time.Second

// More errors of the module, for Telegram.
var (
	// ErrNoEncryption: TOLKOVO_MASTER_KEY is not set, the token cannot be stored.
	ErrNoEncryption = errors.New("notifications: TOLKOVO_MASTER_KEY is not set, the bot token cannot be stored")
	// ErrBotToken: Telegram does not know the token.
	ErrBotToken = errors.New("notifications: Telegram refused the bot token")
	// ErrNoBot: the workspace has no notifications bot.
	ErrNoBot = errors.New("notifications: no Telegram bot for notifications")
	// ErrBotBusy: the bot is a support channel of the workspace, has a
	// webhook or another reader of its updates; it cannot be read with
	// getUpdates.
	ErrBotBusy = errors.New("notifications: the bot's updates are taken by a webhook or another reader")
	// ErrBotRevoked: Telegram no longer knows the saved token; an admin
	// replaces the bot.
	ErrBotRevoked = errors.New("notifications: Telegram no longer knows the bot's token")
	// ErrTelegram: Telegram could not be reached.
	ErrTelegram = errors.New("notifications: Telegram is unavailable")
)

// Bots is the part of the Bot API the module uses; *telegram.BotAPI
// implements it.
type Bots interface {
	GetMe(ctx context.Context, token string) (telegram.Bot, error)
	GetUpdates(ctx context.Context, token string, offset int64, timeout int) ([]telegram.BotUpdate, error)
	// SendNotice sends a text without link previews.
	SendNotice(ctx context.Context, token string, chatID int64, text string) (int64, error)
}

// Tasks reads the task a message is about; *tasks.Service implements it.
type Tasks interface {
	Get(ctx context.Context, workspaceID, taskID uuid.UUID) (tasks.Task, error)
}

// Jobs queues the messages in the caller's transaction; *jobs.Client
// implements it.
type Jobs interface {
	InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// ChannelBots tells whether a bot is a support channel of the workspace;
// *channels.Service implements it.
type ChannelBots interface {
	HasBot(ctx context.Context, workspaceID uuid.UUID, botID int64) (bool, error)
}

// TelegramOptions connect Telegram.
type TelegramOptions struct {
	// Sealer seals the bot token; nil refuses to add a bot (ErrNoEncryption).
	Sealer *crypto.Sealer
	Bots   Bots
	// Channels refuses a bot that serves a support channel: reading its
	// updates would take the customers' messages. Nil checks nothing.
	Channels ChannelBots
	// Jobs queues messages (role api); nil sends none.
	Jobs Jobs
	Log  *slog.Logger
}

// SetTelegram connects Telegram; call it before serving.
func (s *Service) SetTelegram(o TelegramOptions) { s.tg = o }

func (s *Service) tgLog() *slog.Logger {
	if s.tg.Log != nil {
		return s.tg.Log
	}
	return slog.Default()
}

// NotifyBot is the workspace's notifications bot.
type NotifyBot struct {
	Username string
}

// TelegramState is what the settings show: the bot and whether a token can
// be stored at all.
type TelegramState struct {
	Bot                 *NotifyBot
	EncryptionAvailable bool
}

// TelegramState returns the workspace's bot.
func (s *Service) TelegramState(ctx context.Context, workspaceID uuid.UUID) (TelegramState, error) {
	out := TelegramState{EncryptionAvailable: s.tg.Sealer != nil}
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		row, err := store.New(tx).GetNotifyBot(ctx, workspaceID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err == nil {
			out.Bot = &NotifyBot{Username: row.Username}
		}
		return err
	})
	if err != nil {
		return TelegramState{}, fmt.Errorf("notify bot: %w", err)
	}
	return out, nil
}

// SetBot checks the token with Telegram, seals it and makes it the
// workspace's notifications bot. Another bot replaces the old one, and
// every member links again: their chats belong to the old bot.
func (s *Service) SetBot(ctx context.Context, workspaceID, actorID uuid.UUID, token string) (NotifyBot, error) {
	token = strings.TrimSpace(token)
	if !telegram.ValidBotToken(token) {
		return NotifyBot{}, fmt.Errorf("%w: not a bot token from @BotFather", ErrInvalid)
	}
	if s.tg.Sealer == nil {
		return NotifyBot{}, ErrNoEncryption
	}
	if s.tg.Bots == nil {
		return NotifyBot{}, ErrTelegram
	}
	bot, err := s.tg.Bots.GetMe(ctx, token)
	if err != nil {
		return NotifyBot{}, botError(err)
	}
	if s.tg.Channels != nil {
		taken, err := s.tg.Channels.HasBot(ctx, workspaceID, bot.ID)
		if err != nil {
			return NotifyBot{}, err
		}
		if taken {
			return NotifyBot{}, ErrBotBusy
		}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return NotifyBot{}, err
	}
	sealed, err := s.tg.Sealer.Seal(ctx, []byte(token), crypto.AAD{WorkspaceID: workspaceID, SecretID: id})
	if err != nil {
		return NotifyBot{}, fmt.Errorf("seal bot token: %w", err)
	}
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		cur, err := q.GetNotifyBot(ctx, workspaceID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return err
		case cur.BotID != bot.ID:
			if err := q.ClearTelegramLinks(ctx, workspaceID); err != nil {
				return err
			}
		}
		_, err = q.UpsertNotifyBot(ctx, store.UpsertNotifyBotParams{
			WorkspaceID: workspaceID, ID: id, BotID: bot.ID, Username: bot.Username, Sealed: sealed,
			CreatedBy: pgtype.UUID{Bytes: actorID, Valid: true}, CreatedAt: s.now(),
		})
		return err
	})
	if err != nil {
		return NotifyBot{}, fmt.Errorf("store notify bot: %w", err)
	}
	return NotifyBot{Username: bot.Username}, nil
}

// DeleteBot forgets the bot and every member's link to it.
func (s *Service) DeleteBot(ctx context.Context, workspaceID uuid.UUID) error {
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.ClearTelegramLinks(ctx, workspaceID); err != nil {
			return err
		}
		_, err := q.DeleteNotifyBot(ctx, workspaceID)
		return err
	})
	if err != nil {
		return fmt.Errorf("delete notify bot: %w", err)
	}
	return nil
}

// MyTelegram is a member's link to the workspace's bot.
type MyTelegram struct {
	// BotUsername is empty when the workspace has no bot.
	BotUsername string
	Linked      bool
	// Name is the member's name in Telegram, once linked.
	Name     string
	LinkedAt *time.Time
	// Pending: a link code waits for Start.
	Pending bool
}

// MyTelegram returns the member's link.
func (s *Service) MyTelegram(ctx context.Context, workspaceID, userID uuid.UUID) (MyTelegram, error) {
	var out MyTelegram
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		out, err = myTelegram(ctx, store.New(tx), workspaceID, userID, s.now())
		return err
	})
	if err != nil {
		return MyTelegram{}, fmt.Errorf("my telegram: %w", err)
	}
	return out, nil
}

func myTelegram(ctx context.Context, q *store.Queries, workspaceID, userID uuid.UUID, now time.Time) (MyTelegram, error) {
	var out MyTelegram
	bot, err := q.GetNotifyBot(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.BotUsername = bot.Username
	link, err := q.GetTelegramLink(ctx, store.GetTelegramLinkParams{WorkspaceID: workspaceID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Linked, out.Name, out.LinkedAt = link.ChatID.Valid, link.TelegramName.String, link.LinkedAt
	out.Pending = link.CodeExpiresAt != nil && link.CodeExpiresAt.After(now)
	return out, nil
}

// LinkStart is the link the member opens in Telegram.
type LinkStart struct {
	URL       string
	ExpiresAt time.Time
}

// StartLink makes a code for the member, valid for LinkTTL, and returns
// the link to the bot with it. Only the code's hash is stored.
func (s *Service) StartLink(ctx context.Context, workspaceID, userID uuid.UUID) (LinkStart, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return LinkStart{}, err
	}
	code := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(code))
	expires := s.now().Add(LinkTTL)
	var username string
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		bot, err := q.GetNotifyBot(ctx, workspaceID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoBot
		}
		if err != nil {
			return err
		}
		username = bot.Username
		return q.StartTelegramLink(ctx, store.StartTelegramLinkParams{
			WorkspaceID: workspaceID, UserID: userID, CodeHash: hash[:], CodeExpiresAt: &expires,
		})
	})
	if err != nil {
		if errors.Is(err, ErrNoBot) {
			return LinkStart{}, err
		}
		return LinkStart{}, fmt.Errorf("start telegram link: %w", err)
	}
	return LinkStart{URL: "https://t.me/" + url.PathEscape(username) + "?start=" + code, ExpiresAt: expires}, nil
}

// CheckLink reads the bot's new updates and completes every link whose
// code came with /start, then returns the member's link. Without a bot, or
// without a code of the member waiting for Start, it returns the state as
// it is; so it does when the bot was read less than CheckInterval ago.
func (s *Service) CheckLink(ctx context.Context, workspaceID, userID uuid.UUID) (MyTelegram, error) {
	cur, err := s.MyTelegram(ctx, workspaceID, userID)
	if err != nil || !cur.Pending || !s.mayRead(workspaceID) {
		return cur, err
	}
	bot, token, err := s.openBot(ctx, workspaceID)
	if errors.Is(err, ErrNoBot) {
		return s.MyTelegram(ctx, workspaceID, userID)
	}
	if err != nil {
		return MyTelegram{}, err
	}
	updates, err := s.tg.Bots.GetUpdates(ctx, token.Reveal(), bot.UpdatesOffset, 0)
	if err != nil {
		// A member cannot fix the token: they are told an admin can.
		if err = botError(err); errors.Is(err, ErrBotToken) {
			return MyTelegram{}, ErrBotRevoked
		}
		return MyTelegram{}, err
	}
	now := s.now()
	var out MyTelegram
	var linked, refused []int64
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		next := bot.UpdatesOffset
		linked, refused = linked[:0], refused[:0]
		for _, u := range updates {
			next = max(next, u.ID+1)
			code, ok := startCode(u.Message)
			if !ok {
				continue
			}
			hash := sha256.Sum256([]byte(code))
			taken, err := q.ChatLinkedToOther(ctx, store.ChatLinkedToOtherParams{
				WorkspaceID: workspaceID, ChatID: u.Message.ChatID, CodeHash: hash[:], Now: now,
			})
			if err != nil {
				return err
			}
			if taken {
				refused = append(refused, u.Message.ChatID)
				continue
			}
			n, err := q.CompleteTelegramLink(ctx, store.CompleteTelegramLinkParams{
				ChatID: pgtype.Int8{Int64: u.Message.ChatID, Valid: true}, TelegramName: pgtype.Text{String: personName(u.Message.From), Valid: true},
				LinkedAt: &now, WorkspaceID: workspaceID, CodeHash: hash[:],
			})
			if err != nil {
				return err
			}
			if n > 0 {
				linked = append(linked, u.Message.ChatID)
			}
		}
		if next != bot.UpdatesOffset {
			if err := q.AdvanceNotifyOffset(ctx, store.AdvanceNotifyOffsetParams{UpdatesOffset: next, WorkspaceID: workspaceID, ID: bot.ID}); err != nil {
				return err
			}
		}
		var err error
		out, err = myTelegram(ctx, q, workspaceID, userID, now)
		return err
	})
	if err != nil {
		return MyTelegram{}, fmt.Errorf("check telegram link: %w", err)
	}
	s.tell(ctx, token, linked, "Tolkovo: уведомления о задачах будут приходить сюда. / Task notifications will arrive here.")
	s.tell(ctx, token, refused, "Tolkovo: этот Telegram уже привязан к другому сотруднику. / This Telegram is already linked to another member.")
	return out, nil
}

// mayRead reports whether the workspace's bot may be read now, and if so
// marks it read: at most once per CheckInterval.
func (s *Service) mayRead(workspaceID uuid.UUID) bool {
	now := s.now()
	for {
		last, loaded := s.checks.LoadOrStore(workspaceID, now)
		if !loaded {
			return true
		}
		if now.Sub(last.(time.Time)) < CheckInterval {
			return false
		}
		if s.checks.CompareAndSwap(workspaceID, last, now) {
			return true
		}
	}
}

// tell sends text to chats: the greeting of new links or a refusal. Best
// effort, after the links are stored: a lost message changes nothing.
func (s *Service) tell(ctx context.Context, token crypto.Secret, chats []int64, text string) {
	for _, chat := range chats {
		if _, err := s.tg.Bots.SendNotice(ctx, token.Reveal(), chat, text); err != nil {
			s.tgLog().WarnContext(ctx, "telegram notice not sent", "error", err)
		}
	}
}

// Unlink forgets the member's chat and any pending code.
func (s *Service) Unlink(ctx context.Context, workspaceID, userID uuid.UUID) error {
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		return store.New(tx).DeleteTelegramLink(ctx, store.DeleteTelegramLinkParams{WorkspaceID: workspaceID, UserID: userID})
	})
	if err != nil {
		return fmt.Errorf("unlink telegram: %w", err)
	}
	return nil
}

// MemberLeft forgets the Telegram chat of a member who left the team, in
// the caller's transaction tx (ADR-0020).
func (s *Service) MemberLeft(ctx context.Context, tx pgx.Tx, workspaceID, userID uuid.UUID) error {
	if err := store.New(tx).DeleteTelegramLink(ctx, store.DeleteTelegramLinkParams{WorkspaceID: workspaceID, UserID: userID}); err != nil {
		return fmt.Errorf("unlink telegram: %w", err)
	}
	return nil
}

// TaskNotify queues a Telegram message about a task to each member who
// linked their Telegram; it implements tasks.Notifier inside the change's
// transaction.
func (s *Service) TaskNotify(ctx context.Context, tx pgx.Tx, n tasks.Notice) error {
	if s.tg.Jobs == nil || len(n.UserIDs) == 0 {
		return nil
	}
	q := store.New(tx)
	if _, err := q.GetNotifyBot(ctx, n.WorkspaceID); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	chats, err := q.LinkedChats(ctx, store.LinkedChatsParams{WorkspaceID: n.WorkspaceID, UserIds: n.UserIDs})
	if err != nil {
		return err
	}
	for _, c := range chats {
		if _, err := s.tg.Jobs.InsertTx(ctx, tx, TaskMessageArgs{
			WorkspaceID: n.WorkspaceID, TaskID: n.TaskID, UserID: c.UserID, About: n.Kind,
		}, nil); err != nil {
			return fmt.Errorf("queue telegram message: %w", err)
		}
	}
	return nil
}

// TaskMessageArgs sends one message about a task to one member. Only ids:
// the job reads the task and the chat when it runs.
type TaskMessageArgs struct {
	WorkspaceID uuid.UUID `json:"workspace_id"`
	TaskID      uuid.UUID `json:"task_id"`
	UserID      uuid.UUID `json:"user_id"`
	// About is tasks.NoticeAssigned or tasks.NoticeQueued.
	About string `json:"about"`
}

// Kind implements river.JobArgs.
func (TaskMessageArgs) Kind() string { return "notifications.telegram_task" }

// InsertOpts implements river.JobArgsWithInsertOpts: Telegram may be away
// for a while, a message an hour late is still useful. River's backoff
// (attempt⁴ seconds) spreads 8 attempts over about 80 minutes; a 429 waits
// as long as Telegram asks without spending one.
func (TaskMessageArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 8} }

// TelegramWorkerOptions configure the sending jobs (role worker).
type TelegramWorkerOptions struct {
	Service *Service
	Tasks   Tasks
	// Conversations and Panels send the messages about conversations
	// (chats.go); nil sends none of them.
	Conversations Conversations
	Panels        Panels
	// Locale is the language of a member, "ru" or "en"; nil writes Russian.
	Locale func(ctx context.Context, workspaceID, userID uuid.UUID) (string, error)
	// PanelURL is where the panel lives; the message links to the task.
	PanelURL string
}

// AddTelegramWorkers registers the sending jobs.
func AddTelegramWorkers(workers *river.Workers, o TelegramWorkerOptions) error {
	if o.Service == nil || o.Tasks == nil || o.Service.tg.Bots == nil || o.Service.tg.Sealer == nil {
		return errors.New("notifications: telegram workers need the service with Telegram, and tasks")
	}
	river.AddWorker(workers, &taskMessageWorker{opts: o})
	if o.Conversations != nil && o.Panels != nil {
		river.AddWorker(workers, &chatMessageWorker{opts: o})
	}
	return nil
}

type taskMessageWorker struct {
	river.WorkerDefaults[TaskMessageArgs]
	opts TelegramWorkerOptions
}

// Timeout bounds one message.
func (w *taskMessageWorker) Timeout(*river.Job[TaskMessageArgs]) time.Duration {
	return 30 * time.Second
}

// Work implements river.Worker.
func (w *taskMessageWorker) Work(ctx context.Context, job *river.Job[TaskMessageArgs]) error {
	return SendTaskMessage(ctx, w.opts, job.Args)
}

// SendTaskMessage sends one message about a task (the job's work). The bot
// gone, the member unlinked, the task deleted: it ends quietly. A member
// who blocked the bot is unlinked.
func SendTaskMessage(ctx context.Context, o TelegramWorkerOptions, a TaskMessageArgs) error {
	s := o.Service
	_, token, err := s.openBot(ctx, a.WorkspaceID)
	if errors.Is(err, ErrNoBot) {
		return nil
	}
	if err != nil {
		return err
	}
	var chat int64
	err = s.db.InTenantTx(ctx, a.WorkspaceID, func(tx pgx.Tx) error {
		rows, err := store.New(tx).LinkedChats(ctx, store.LinkedChatsParams{WorkspaceID: a.WorkspaceID, UserIds: []uuid.UUID{a.UserID}})
		if err == nil && len(rows) == 1 {
			chat = rows[0].ChatID
		}
		return err
	})
	if err != nil || chat == 0 {
		return err
	}
	task, err := o.Tasks.Get(ctx, a.WorkspaceID, a.TaskID)
	if errors.Is(err, tasks.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	locale := "ru"
	if o.Locale != nil {
		if l, err := o.Locale(ctx, a.WorkspaceID, a.UserID); err == nil && l == "en" {
			locale = l
		}
	}
	_, err = s.tg.Bots.SendNotice(ctx, token.Reveal(), chat, taskMessage(locale, a.About, task, strings.TrimRight(o.PanelURL, "/")))
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

// taskMessage is the text of a message: the number, the title and the
// link, nothing of the description or the customer.
func taskMessage(locale, kind string, t tasks.Task, panel string) string {
	head := fmt.Sprintf("Вам назначена задача #%d", t.Number)
	if kind == tasks.NoticeQueued {
		head = fmt.Sprintf("Новая задача в очереди: #%d", t.Number)
	}
	if locale == "en" {
		head = fmt.Sprintf("Task #%d is assigned to you", t.Number)
		if kind == tasks.NoticeQueued {
			head = fmt.Sprintf("New task in the queue: #%d", t.Number)
		}
	}
	text := head + "\n" + t.Title
	if panel != "" {
		text += "\n" + panel + "/tasks/" + t.ID.String()
	}
	return text
}

// openBot reads and opens the workspace's bot token.
func (s *Service) openBot(ctx context.Context, workspaceID uuid.UUID) (store.NotifyBot, crypto.Secret, error) {
	if s.tg.Sealer == nil || s.tg.Bots == nil {
		return store.NotifyBot{}, crypto.Secret{}, ErrNoBot
	}
	var bot store.NotifyBot
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		bot, err = store.New(tx).GetNotifyBot(ctx, workspaceID)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return store.NotifyBot{}, crypto.Secret{}, ErrNoBot
	}
	if err != nil {
		return store.NotifyBot{}, crypto.Secret{}, fmt.Errorf("load notify bot: %w", err)
	}
	token, err := s.tg.Sealer.Open(ctx, bot.Sealed, crypto.AAD{WorkspaceID: workspaceID, SecretID: bot.ID})
	if err != nil {
		return store.NotifyBot{}, crypto.Secret{}, fmt.Errorf("open bot token: %w", err)
	}
	return bot, token, nil
}

// startCode is the code of a "/start <code>" message.
func startCode(m *telegram.Message) (string, bool) {
	if m == nil || m.Out {
		return "", false
	}
	rest, ok := strings.CutPrefix(strings.TrimSpace(m.Text), "/start ")
	code := strings.TrimSpace(rest)
	if !ok || code == "" || len(code) > 64 {
		return "", false
	}
	return code, true
}

func personName(p telegram.Person) string {
	name := p.Name()
	if p.Username != "" {
		if name != "" {
			name += " "
		}
		name += "@" + p.Username
	}
	r := []rune(name)
	if len(r) > 200 {
		r = r[:200]
	}
	return string(r)
}

// botError maps errors of the Bot API.
func botError(err error) error {
	var apiErr *telegram.APIError
	switch {
	case errors.Is(err, telegram.ErrBotToken):
		return ErrBotToken
	case errors.As(err, &apiErr) && apiErr.Code == 409:
		return ErrBotBusy
	case errors.As(err, &apiErr) && (apiErr.Code == 401 || apiErr.Code == 404):
		return ErrBotToken
	}
	return fmt.Errorf("%w: %w", ErrTelegram, err)
}
