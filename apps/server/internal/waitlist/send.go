package waitlist

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"net/url"
	"strings"
	texttemplate "text/template"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/mail"
	"github.com/tolkovo/tolkovo/apps/server/internal/waitlist/store"
)

// SendConfirmationArgs is the job that emails a confirmation link. It
// carries only the entry ID: the address stays out of the job table.
type SendConfirmationArgs struct {
	EntryID uuid.UUID `json:"entry_id"`
}

// Kind implements river.JobArgs.
func (SendConfirmationArgs) Kind() string { return "waitlist.send_confirmation" }

// InsertOpts keeps one queued email per entry: repeated submissions while
// one is waiting do not stack up.
func (SendConfirmationArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: 5,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRetryable,
				rivertype.JobStateRunning, rivertype.JobStateScheduled,
			},
		},
	}
}

// SendOptions configure NewSendWorker.
type SendOptions struct {
	// SiteURL is the landing that hosts the confirm and unsubscribe pages.
	SiteURL string
	// TokenTTL is how long the confirmation link works.
	TokenTTL time.Duration
	// PendingRetention is when an unconfirmed entry is deleted; the email
	// tells the reader.
	PendingRetention time.Duration
	// Now defaults to time.Now.
	Now func() time.Time
}

// SendWorker issues fresh tokens and emails the links. Tokens are stored in
// a short transaction; the email goes out after it commits (no network
// inside a transaction, ADR-0004). A retry issues new tokens, so only the
// latest email's links work.
type SendWorker struct {
	river.WorkerDefaults[SendConfirmationArgs]
	db     *db.DB
	sender mail.Sender
	opts   SendOptions
	site   *url.URL
}

// NewSendWorker builds the worker.
func NewSendWorker(database *db.DB, sender mail.Sender, o SendOptions) (*SendWorker, error) {
	site, err := url.Parse(o.SiteURL)
	if err != nil || site.Host == "" {
		return nil, errors.New("waitlist: site URL must be absolute")
	}
	if o.TokenTTL <= 0 || o.PendingRetention <= 0 {
		return nil, errors.New("waitlist: token TTL and pending retention must be positive")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &SendWorker{db: database, sender: sender, opts: o, site: site}, nil
}

// Timeout bounds one SMTP delivery.
func (w *SendWorker) Timeout(*river.Job[SendConfirmationArgs]) time.Duration { return time.Minute }

// Work implements river.Worker.
func (w *SendWorker) Work(ctx context.Context, job *river.Job[SendConfirmationArgs]) error {
	confirmToken, confirmHash, err := newToken()
	if err != nil {
		return err
	}
	unsubToken, unsubHash, err := newToken()
	if err != nil {
		return err
	}
	now := w.opts.Now().UTC()
	var to store.IssueTokensRow
	err = w.db.InGlobalTx(ctx, func(tx pgx.Tx) error {
		to, err = store.New(tx).IssueTokens(ctx, store.IssueTokensParams{
			ID: job.Args.EntryID, ConfirmHash: confirmHash, UnsubscribeHash: unsubHash,
			ExpiresAt: now.Add(w.opts.TokenTTL), Now: now, MaxSends: MaxSends,
		})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // confirmed, deleted or out of sends meanwhile
	}
	if err != nil {
		return fmt.Errorf("waitlist: issue tokens for %s: %w", job.Args.EntryID, err)
	}
	msg, err := w.render(to.Email, to.Locale, confirmToken, unsubToken)
	if err != nil {
		return err
	}
	if err := w.sender.Send(ctx, msg); err != nil {
		return fmt.Errorf("waitlist: send confirmation for %s: %w", job.Args.EntryID, err)
	}
	return nil
}

//go:embed templates
var templateFS embed.FS

var (
	textTemplates = texttemplate.Must(texttemplate.ParseFS(templateFS, "templates/*.txt"))
	htmlTemplates = htmltemplate.Must(htmltemplate.ParseFS(templateFS, "templates/*.html"))
)

// subjects per locale; bodies are templates/confirm.<locale>.{txt,html}.
var subjects = map[string]string{
	"ru": "Подтвердите адрес для очереди Tolkovo",
	"en": "Confirm your email for the Tolkovo waitlist",
}

type emailData struct {
	Site           string
	ConfirmURL     string
	UnsubscribeURL string
	Hours          int
	Days           int
}

func (w *SendWorker) render(to, locale, confirmToken, unsubToken string) (mail.Message, error) {
	subject, ok := subjects[locale]
	if !ok {
		locale, subject = "ru", subjects["ru"]
	}
	data := emailData{
		Site:           w.site.Host,
		ConfirmURL:     w.pageURL(locale, "confirm", confirmToken),
		UnsubscribeURL: w.pageURL(locale, "unsubscribe", unsubToken),
		Hours:          int(w.opts.TokenTTL / time.Hour),
		Days:           int(w.opts.PendingRetention / (24 * time.Hour)),
	}
	var text, html bytes.Buffer
	if err := textTemplates.ExecuteTemplate(&text, "confirm."+locale+".txt", data); err != nil {
		return mail.Message{}, fmt.Errorf("waitlist: render text: %w", err)
	}
	if err := htmlTemplates.ExecuteTemplate(&html, "confirm."+locale+".html", data); err != nil {
		return mail.Message{}, fmt.Errorf("waitlist: render html: %w", err)
	}
	return mail.Message{To: to, Subject: subject, Text: text.String(), HTML: html.String()}, nil
}

// pageURL links to a landing page with the token in the fragment: browsers
// do not send fragments, so the token reaches neither the static host's
// logs nor Referer headers. ru lives at the root, en under /en/ (docs/05 §7.2).
func (w *SendWorker) pageURL(locale, page, token string) string {
	prefix := "/"
	if locale == "en" {
		prefix = "/en/"
	}
	return strings.TrimRight(w.site.String(), "/") + prefix + "waitlist/" + page + "/#" + token
}
