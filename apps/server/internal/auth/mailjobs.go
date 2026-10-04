package auth

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
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/mail"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// Emails of the auth module go out from River jobs (ADR-0004). A job
// carries only IDs: addresses and tokens stay out of the job table. The
// worker issues the link token right before sending, so a retry sends a new
// link and only the latest one works.

// Lifetimes of emailed links.
const (
	InvitationTTL        = 7 * 24 * time.Hour
	PasswordResetTTL     = time.Hour
	EmailVerificationTTL = 48 * time.Hour
)

// Panel pages that open the links; the token follows in the fragment.
const (
	invitePath      = "/invite"
	resetPath       = "/reset-password"
	verifyEmailPath = "/verify-email"
)

// mailInsertOpts: no uniqueness. A just-sent job may still look running to
// River for a moment, which would swallow a deliberate resend; the rate
// limits of the endpoints bound the number of emails instead.
func mailInsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 5} }

// InvitationEmailArgs emails the link of an open invitation.
type InvitationEmailArgs struct {
	WorkspaceID  uuid.UUID `json:"workspace_id"`
	InvitationID uuid.UUID `json:"invitation_id"`
}

// Kind implements river.JobArgs.
func (InvitationEmailArgs) Kind() string { return "auth.invitation_email" }

// InsertOpts implements river.JobArgsWithInsertOpts.
func (InvitationEmailArgs) InsertOpts() river.InsertOpts { return mailInsertOpts() }

// PasswordResetEmailArgs emails a password reset link.
type PasswordResetEmailArgs struct {
	UserID uuid.UUID `json:"user_id"`
}

// Kind implements river.JobArgs.
func (PasswordResetEmailArgs) Kind() string { return "auth.password_reset_email" }

// InsertOpts implements river.JobArgsWithInsertOpts.
func (PasswordResetEmailArgs) InsertOpts() river.InsertOpts { return mailInsertOpts() }

// EmailVerificationArgs emails an email confirmation link.
type EmailVerificationArgs struct {
	UserID uuid.UUID `json:"user_id"`
}

// Kind implements river.JobArgs.
func (EmailVerificationArgs) Kind() string { return "auth.email_verification" }

// InsertOpts implements river.JobArgsWithInsertOpts.
func (EmailVerificationArgs) InsertOpts() river.InsertOpts { return mailInsertOpts() }

// MailOptions configure AddMailWorkers.
type MailOptions struct {
	Tenancy *tenancy.Service
	Redis   *redis.Client
	Sender  mail.Sender
	// PanelURL is where the panel's link pages live, e.g. https://app.example.com.
	PanelURL string
	// KeyPrefix must match the one of the api's auth service.
	KeyPrefix string
	// Now defaults to time.Now.
	Now func() time.Time
}

// AddMailWorkers registers the email jobs of the auth module. Only the
// worker role calls it, and only with outgoing email configured.
func AddMailWorkers(workers *river.Workers, o MailOptions) error {
	if o.Tenancy == nil || o.Redis == nil || o.Sender == nil {
		return errors.New("auth: mail workers need tenancy, redis and a sender")
	}
	links, err := newLinkBuilder(o.PanelURL)
	if err != nil {
		return err
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	m := &mailer{tenancy: o.Tenancy, sender: o.Sender, links: links, now: o.Now}
	river.AddWorker(workers, &invitationWorker{mailer: m})
	river.AddWorker(workers, &passwordResetWorker{mailer: m, tokens: resetTokens(o.Redis, o.KeyPrefix)})
	river.AddWorker(workers, &verificationWorker{mailer: m, tokens: verifyTokens(o.Redis, o.KeyPrefix)})
	return nil
}

func resetTokens(rdb *redis.Client, prefix string) userTokens {
	return userTokens{rdb: rdb, prefix: prefix, purpose: "pwreset", ttl: PasswordResetTTL}
}

func verifyTokens(rdb *redis.Client, prefix string) userTokens {
	return userTokens{rdb: rdb, prefix: prefix, purpose: "emailverify", ttl: EmailVerificationTTL}
}

// linkBuilder turns tokens into panel links.
type linkBuilder struct{ base string }

func newLinkBuilder(panelURL string) (linkBuilder, error) {
	u, err := url.Parse(panelURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return linkBuilder{}, fmt.Errorf("auth: panel URL must be an absolute http(s) URL without query, got %q", panelURL)
	}
	return linkBuilder{base: strings.TrimRight(u.String(), "/")}, nil
}

func (b linkBuilder) link(path, token string) string { return b.base + path + "#" + token }

type mailer struct {
	tenancy *tenancy.Service
	sender  mail.Sender
	links   linkBuilder
	now     func() time.Time
}

type invitationWorker struct {
	river.WorkerDefaults[InvitationEmailArgs]
	*mailer
}

// Timeout bounds one SMTP delivery.
func (w *invitationWorker) Timeout(*river.Job[InvitationEmailArgs]) time.Duration { return time.Minute }

// Work implements river.Worker.
func (w *invitationWorker) Work(ctx context.Context, job *river.Job[InvitationEmailArgs]) error {
	token, hash, err := newLinkToken(job.Args.WorkspaceID)
	if err != nil {
		return err
	}
	inv, err := w.tenancy.IssueInvitationToken(ctx, job.Args.WorkspaceID, job.Args.InvitationID, hash,
		w.now().Add(InvitationTTL), nil)
	if errors.Is(err, tenancy.ErrInvitationNotFound) {
		return nil // accepted, revoked or the workspace is gone meanwhile
	}
	if err != nil {
		return err
	}
	msg, err := renderMail(inv.WorkspaceLocale, "invitation", inv.Email, mailData{
		Link: w.links.link(invitePath, token), Workspace: inv.WorkspaceName, Inviter: inv.InvitedByName,
		Role: roleName(inv.WorkspaceLocale, inv.Role), Days: int(InvitationTTL / (24 * time.Hour)),
	})
	if err != nil {
		return err
	}
	if err := w.sender.Send(ctx, msg); err != nil {
		return fmt.Errorf("auth: send invitation %s: %w", job.Args.InvitationID, err)
	}
	return nil
}

type passwordResetWorker struct {
	river.WorkerDefaults[PasswordResetEmailArgs]
	*mailer
	tokens userTokens
}

// Timeout bounds one SMTP delivery.
func (w *passwordResetWorker) Timeout(*river.Job[PasswordResetEmailArgs]) time.Duration {
	return time.Minute
}

// Work implements river.Worker.
func (w *passwordResetWorker) Work(ctx context.Context, job *river.Job[PasswordResetEmailArgs]) error {
	u, err := w.tenancy.User(ctx, job.Args.UserID)
	if errors.Is(err, tenancy.ErrNotFound) {
		return nil // disabled or deleted meanwhile
	}
	if err != nil {
		return err
	}
	token, err := w.tokens.issue(ctx, u.ID)
	if err != nil {
		return err
	}
	msg, err := renderMail(u.Locale, "reset", u.Email, mailData{
		Link: w.links.link(resetPath, token), Name: u.Name, Minutes: int(PasswordResetTTL / time.Minute),
	})
	if err != nil {
		return err
	}
	if err := w.sender.Send(ctx, msg); err != nil {
		return fmt.Errorf("auth: send password reset to user %s: %w", u.ID, err)
	}
	return nil
}

type verificationWorker struct {
	river.WorkerDefaults[EmailVerificationArgs]
	*mailer
	tokens userTokens
}

// Timeout bounds one SMTP delivery.
func (w *verificationWorker) Timeout(*river.Job[EmailVerificationArgs]) time.Duration {
	return time.Minute
}

// Work implements river.Worker.
func (w *verificationWorker) Work(ctx context.Context, job *river.Job[EmailVerificationArgs]) error {
	u, err := w.tenancy.User(ctx, job.Args.UserID)
	if errors.Is(err, tenancy.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if u.EmailVerifiedAt != nil {
		return nil // confirmed meanwhile
	}
	token, err := w.tokens.issue(ctx, u.ID)
	if err != nil {
		return err
	}
	msg, err := renderMail(u.Locale, "verify", u.Email, mailData{
		Link: w.links.link(verifyEmailPath, token), Name: u.Name, Hours: int(EmailVerificationTTL / time.Hour),
	})
	if err != nil {
		return err
	}
	if err := w.sender.Send(ctx, msg); err != nil {
		return fmt.Errorf("auth: send email confirmation to user %s: %w", u.ID, err)
	}
	return nil
}

// mailData fills the templates below.
type mailData struct {
	Link      string
	Name      string
	Workspace string
	Inviter   string
	Role      string
	Days      int
	Hours     int
	Minutes   int
}

var mailSubjects = map[string]map[string]string{
	"invitation": {"ru": "Приглашение в «{{.Workspace}}» в Tolkovo", "en": "Join {{.Workspace}} on Tolkovo"},
	"reset":      {"ru": "Сброс пароля Tolkovo", "en": "Reset your Tolkovo password"},
	"verify":     {"ru": "Подтвердите почту для Tolkovo", "en": "Confirm your email for Tolkovo"},
}

var mailBodies = map[string]map[string]string{
	"invitation": {
		"ru": `Здравствуйте!

{{if .Inviter}}{{.Inviter}} приглашает вас{{else}}Вас приглашают{{end}} в пространство «{{.Workspace}}» в Tolkovo. Роль: {{.Role}}.

Принять приглашение:
{{.Link}}

Ссылка действует {{.Days}} дней. Если вы не ждали этого письма, просто удалите его.
`,
		"en": `Hello,

{{if .Inviter}}{{.Inviter}} invites you{{else}}You are invited{{end}} to the {{.Workspace}} workspace on Tolkovo. Role: {{.Role}}.

Accept the invitation:
{{.Link}}

The link works for {{.Days}} days. If you did not expect this email, just delete it.
`,
	},
	"reset": {
		"ru": `Здравствуйте, {{.Name}}!

Кто-то, возможно вы, попросил сбросить пароль Tolkovo. Задать новый пароль:
{{.Link}}

Ссылка действует {{.Minutes}} минут и срабатывает один раз. После смены пароля все сеансы завершатся.
Если вы не просили сброс, ничего не делайте: пароль останется прежним.
`,
		"en": `Hello {{.Name}},

Someone, probably you, asked to reset your Tolkovo password. Set a new one:
{{.Link}}

The link works once, for {{.Minutes}} minutes. Changing the password signs you out everywhere.
If you did not ask for this, do nothing: your password stays the same.
`,
	},
	"verify": {
		"ru": `Здравствуйте, {{.Name}}!

Подтвердите, что это ваш адрес, — на него придут сброс пароля и важные уведомления:
{{.Link}}

Ссылка действует {{.Hours}} часов. Если вы не регистрировались в Tolkovo, просто удалите письмо.
`,
		"en": `Hello {{.Name}},

Confirm that this address is yours: password resets and important notices will come here.
{{.Link}}

The link works for {{.Hours}} hours. If you did not sign up for Tolkovo, just delete this email.
`,
	},
}

var mailTemplates = func() map[string]*template.Template {
	out := map[string]*template.Template{}
	for kind, locales := range mailBodies {
		for locale, body := range locales {
			name := kind + "." + locale
			t := template.Must(template.New(name + ".subject").Parse(mailSubjects[kind][locale]))
			template.Must(t.New(name + ".body").Parse(body))
			out[name] = t
		}
	}
	return out
}()

func renderMail(locale, kind, to string, data mailData) (mail.Message, error) {
	if locale != "en" {
		locale = "ru"
	}
	t, ok := mailTemplates[kind+"."+locale]
	if !ok {
		return mail.Message{}, fmt.Errorf("auth: no %s email template", kind)
	}
	var subject, body bytes.Buffer
	if err := t.ExecuteTemplate(&subject, kind+"."+locale+".subject", data); err != nil {
		return mail.Message{}, fmt.Errorf("auth: render %s subject: %w", kind, err)
	}
	if err := t.ExecuteTemplate(&body, kind+"."+locale+".body", data); err != nil {
		return mail.Message{}, fmt.Errorf("auth: render %s body: %w", kind, err)
	}
	// A header must stay on one line, whatever a workspace name contains.
	subj := strings.Join(strings.Fields(subject.String()), " ")
	return mail.Message{To: to, Subject: subj, Text: body.String()}, nil
}

func roleName(locale, role string) string {
	names := map[string]map[string]string{
		"ru": {tenancy.RoleAdmin: "администратор", tenancy.RoleOperator: "оператор"},
		"en": {tenancy.RoleAdmin: "admin", tenancy.RoleOperator: "operator"},
	}
	if locale != "en" {
		locale = "ru"
	}
	if n, ok := names[locale][role]; ok {
		return n
	}
	return role
}
