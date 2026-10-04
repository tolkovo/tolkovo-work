package notifications

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"text/template"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/notifications/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/mail"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// MissedEmailArgs emails a member about unread customer messages in a
// conversation of theirs. Only ids: addresses and texts stay out of the
// job table, and the email carries no message text either.
type MissedEmailArgs struct {
	WorkspaceID    uuid.UUID `json:"workspace_id"`
	ConversationID uuid.UUID `json:"conversation_id"`
	UserID         uuid.UUID `json:"user_id"`
}

// Kind implements river.JobArgs.
func (MissedEmailArgs) Kind() string { return "notifications.missed_email" }

// InsertOpts implements river.JobArgsWithInsertOpts.
func (MissedEmailArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 5} }

// Panel pages the email links to.
const (
	inboxPath    = "/inbox/"
	settingsPath = "/settings/notifications"
)

// WorkerOptions configure AddWorkers.
type WorkerOptions struct {
	Service       *Service
	Tenancy       *tenancy.Service
	Conversations *conversations.Service
	Sender        mail.Sender
	// PanelURL is where the panel lives, e.g. https://app.example.com.
	PanelURL string
}

// AddWorkers registers the email job. Only the worker role calls it, and
// only with outgoing email configured.
func AddWorkers(workers *river.Workers, o WorkerOptions) error {
	if o.Service == nil || o.Tenancy == nil || o.Conversations == nil || o.Sender == nil {
		return errors.New("notifications: workers need the service, tenancy, conversations and a sender")
	}
	u, err := url.Parse(o.PanelURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("notifications: panel URL must be an absolute http(s) URL without query, got %q", o.PanelURL)
	}
	river.AddWorker(workers, &missedEmailWorker{opts: o, panel: strings.TrimRight(u.String(), "/")})
	return nil
}

type missedEmailWorker struct {
	river.WorkerDefaults[MissedEmailArgs]
	opts  WorkerOptions
	panel string
}

// Timeout bounds one SMTP delivery.
func (w *missedEmailWorker) Timeout(*river.Job[MissedEmailArgs]) time.Duration { return time.Minute }

// Work implements river.Worker. Every check that fails ends the job
// quietly: the member read the messages, the conversation moved on, the
// member left or turned the email off meanwhile.
func (w *missedEmailWorker) Work(ctx context.Context, job *river.Job[MissedEmailArgs]) error {
	a := job.Args
	m, err := w.opts.Tenancy.Membership(ctx, a.WorkspaceID, a.UserID)
	if errors.Is(err, tenancy.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	// An unconfirmed address may be a typo: nobody else's inbox gets these.
	if m.User.EmailVerifiedAt == nil {
		return nil
	}
	prefs, err := w.opts.Service.Prefs(ctx, a.WorkspaceID, a.UserID)
	if err != nil || !prefs.EmailMissed {
		return err
	}
	c, err := w.opts.Conversations.Conversation(ctx, a.WorkspaceID, a.UserID, a.ConversationID)
	if errors.Is(err, conversations.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.AssigneeID == nil || *c.AssigneeID != a.UserID || c.UnreadCount == 0 ||
		c.Status == conversations.StatusResolved || c.Status == conversations.StatusClosed {
		return nil
	}
	covered, err := w.opts.Service.missedMark(ctx, a.WorkspaceID, a.ConversationID, a.UserID)
	if err != nil {
		return err
	}
	if !c.LastMessageAt.After(covered) {
		return nil // these messages were in the previous email
	}
	msg, err := renderMissed(m.User.Locale, m.User.Email, missedData{
		Name: m.User.Name, Workspace: m.Workspace.Name, Number: c.Number, Unread: c.UnreadCount,
		Link: w.panel + inboxPath + c.ID.String(), Settings: w.panel + settingsPath,
	})
	if err != nil {
		return err
	}
	if err := w.opts.Sender.Send(ctx, msg); err != nil {
		return fmt.Errorf("notifications: send missed email to user %s: %w", a.UserID, err)
	}
	// After the send: a failure here repeats the email on retry, which is
	// better than losing it.
	return w.opts.Service.setMissedMark(ctx, a.WorkspaceID, a.ConversationID, a.UserID, c.LastMessageAt)
}

// missedMark is what the last email covered; zero time before any.
func (s *Service) missedMark(ctx context.Context, workspaceID, conversationID, userID uuid.UUID) (time.Time, error) {
	var t time.Time
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		t, err = store.New(tx).GetMissedMark(ctx, store.GetMissedMarkParams{
			WorkspaceID: workspaceID, ConversationID: conversationID, UserID: userID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("missed email mark: %w", err)
	}
	return t, nil
}

func (s *Service) setMissedMark(ctx context.Context, workspaceID, conversationID, userID uuid.UUID, upTo time.Time) error {
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		return store.New(tx).SetMissedMark(ctx, store.SetMissedMarkParams{
			WorkspaceID: workspaceID, ConversationID: conversationID, UserID: userID, CoveredUpTo: upTo, SentAt: s.now(),
		})
	})
	if err != nil {
		return fmt.Errorf("set missed email mark: %w", err)
	}
	return nil
}

type missedData struct {
	Name      string
	Workspace string
	Number    int64
	Unread    int
	Link      string
	Settings  string
}

var missedSubjects = map[string]string{
	"ru": "Непрочитанные сообщения в диалоге №{{.Number}}",
	"en": "Unread messages in conversation #{{.Number}}",
}

var missedBodies = map[string]string{
	"ru": `Здравствуйте, {{.Name}}!

В вашем диалоге №{{.Number}} в пространстве «{{.Workspace}}» есть непрочитанные сообщения клиента: {{.Unread}}.

Открыть диалог:
{{.Link}}

Отключить эти письма или изменить, через сколько минут они приходят:
{{.Settings}}
`,
	"en": `Hello {{.Name}},

Your conversation #{{.Number}} in the {{.Workspace}} workspace has unread customer messages: {{.Unread}}.

Open the conversation:
{{.Link}}

Turn these emails off or change how soon they come:
{{.Settings}}
`,
}

var missedTemplates = func() map[string]*template.Template {
	out := map[string]*template.Template{}
	for locale, body := range missedBodies {
		t := template.Must(template.New("subject").Parse(missedSubjects[locale]))
		template.Must(t.New("body").Parse(body))
		out[locale] = t
	}
	return out
}()

func renderMissed(locale, to string, data missedData) (mail.Message, error) {
	t, ok := missedTemplates[locale]
	if !ok {
		t = missedTemplates["ru"]
	}
	var subject, body bytes.Buffer
	if err := t.ExecuteTemplate(&subject, "subject", data); err != nil {
		return mail.Message{}, fmt.Errorf("notifications: render subject: %w", err)
	}
	if err := t.ExecuteTemplate(&body, "body", data); err != nil {
		return mail.Message{}, fmt.Errorf("notifications: render body: %w", err)
	}
	return mail.Message{To: to, Subject: strings.Join(strings.Fields(subject.String()), " "), Text: body.String()}, nil
}
