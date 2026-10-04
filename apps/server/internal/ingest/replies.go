package ingest

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/ingest/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/ratelimit"
)

// The app user's side of the conversation (docs/02 §11.6, AP-06): the app
// polls the replies to a report and writes back into the same conversation.
// The ingest key alone is extractable from the app, so both need the
// report's token too.

// Rates (docs/02 §13.4): per report, after the token is checked, so nobody
// else can use up a report's rate; per IP before the key is looked up.
var (
	repliesPerReport  = ratelimit.Rule{Limit: 2, Window: time.Minute}
	messagesPerReport = ratelimit.Rule{Limit: 10, Window: time.Hour}
	threadPerIP       = ratelimit.Rule{Limit: 120, Window: time.Minute}
)

// processingRetry: how soon to try again while a report is being processed.
const processingRetry = 5 * time.Second

// ErrProcessing means the report is not a conversation yet.
var ErrProcessing = errors.New("ingest: the report is still being processed")

// Replies is a page of replies to the app user.
type Replies struct {
	conversations.ReportReplies
	// Rate is the report's rate, for the RateLimit-* headers.
	Rate ratelimit.Result
}

// Replies returns what operators and the bot wrote to the app user after
// the cursor since. Before the report is a conversation the page is empty.
func (s *Service) Replies(ctx context.Context, c Caller, reportID uuid.UUID, token, since string) (Replies, error) {
	report, rate, err := s.openReport(ctx, c, reportID, token, "ingest:replies:", repliesPerReport)
	if err != nil {
		return Replies{Rate: rate}, err
	}
	if report.Status != StatusProcessed {
		return Replies{ReportReplies: conversations.ReportReplies{Cursor: since}, Rate: rate}, nil
	}
	page, err := s.opts.Inbox.ReportReplies(ctx, report.WorkspaceID, *report.ConversationID, since)
	if errors.Is(err, conversations.ErrInvalid) {
		return Replies{Rate: rate}, fmt.Errorf("%w: since is not a cursor of this API", ErrInvalid)
	}
	if err != nil {
		return Replies{Rate: rate}, err
	}
	return Replies{ReportReplies: page, Rate: rate}, nil
}

// Posted is a stored message of the app user.
type Posted struct {
	conversations.ReportMessageReceived
	Rate ratelimit.Result
}

// Reply stores what the app user wrote back. A retry with the same
// Idempotency-Key and text returns the first message.
func (s *Service) Reply(ctx context.Context, c Caller, reportID uuid.UUID, token, idempotencyKey, message string) (Posted, error) {
	report, rate, err := s.openReport(ctx, c, reportID, token, "ingest:messages:", messagesPerReport)
	if err != nil {
		return Posted{Rate: rate}, err
	}
	switch {
	case strings.TrimSpace(message) == "" || utf8.RuneCountInString(message) > MaxMessageChars || !utf8.ValidString(message):
		return Posted{Rate: rate}, fmt.Errorf("%w: message must be 1 to %d characters", ErrInvalid, MaxMessageChars)
	case len(idempotencyKey) > 255:
		return Posted{Rate: rate}, fmt.Errorf("%w: Idempotency-Key must be at most 255 characters", ErrInvalid)
	case report.Status != StatusProcessed:
		return Posted{Rate: rate}, &ProcessingError{RetryAfter: processingRetry}
	}
	if idempotencyKey == "" {
		idempotencyKey = uuid.NewString()
	}
	got, err := s.opts.Inbox.ReceiveReportMessage(ctx, report.WorkspaceID, conversations.ReportMessage{
		ConversationID: *report.ConversationID, Key: idempotencyKey, Body: message,
	})
	switch {
	case errors.Is(err, conversations.ErrIdempotencyMismatch):
		return Posted{Rate: rate}, ErrIdempotencyMismatch
	case errors.Is(err, conversations.ErrNotFound):
		return Posted{Rate: rate}, ErrNotFound // the conversation was deleted with its contact
	case err != nil:
		return Posted{Rate: rate}, err
	}
	return Posted{ReportMessageReceived: got, Rate: rate}, nil
}

// ProcessingError is ErrProcessing with the time to wait.
type ProcessingError struct{ RetryAfter time.Duration }

func (e *ProcessingError) Error() string { return ErrProcessing.Error() }

// Unwrap makes errors.Is(err, ErrProcessing) work.
func (e *ProcessingError) Unwrap() error { return ErrProcessing }

// openReport authenticates the key and the report's token and takes one
// request of the report's rate. A report of another channel, a wrong token
// and an expired report are all ErrNotFound.
func (s *Service) openReport(
	ctx context.Context, c Caller, reportID uuid.UUID, token, ratePrefix string, rule ratelimit.Rule,
) (store.AppReport, ratelimit.Result, error) {
	if err := s.allow(ctx, "ingest:thread-ip:"+c.IP, threadPerIP); err != nil {
		return store.AppReport{}, ratelimit.Result{}, err
	}
	app, err := s.opts.Keys.AuthenticateIngestKey(ctx, c.Key)
	if err != nil {
		return store.AppReport{}, ratelimit.Result{}, err
	}
	if s.opts.Inbox == nil {
		return store.AppReport{}, ratelimit.Result{}, errors.New("ingest: no inbox to read conversations")
	}
	var report store.AppReport
	err = s.db.InTenantTx(ctx, app.WorkspaceID, func(tx pgx.Tx) error {
		var err error
		report, err = store.New(tx).GetReport(ctx, store.GetReportParams{WorkspaceID: app.WorkspaceID, ChannelID: app.ChannelID, ID: reportID})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return store.AppReport{}, ratelimit.Result{}, ErrNotFound
	}
	if err != nil {
		return store.AppReport{}, ratelimit.Result{}, fmt.Errorf("load report: %w", err)
	}
	tokenHash := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(report.TokenHash, tokenHash[:]) != 1 || report.Status == StatusExpired ||
		(report.Status == StatusProcessed && report.ConversationID == nil) {
		return store.AppReport{}, ratelimit.Result{}, ErrNotFound
	}
	rate, err := s.limiter.Hit(ctx, ratePrefix+report.ID.String(), rule)
	if err != nil {
		return store.AppReport{}, ratelimit.Result{}, err
	}
	if !rate.Allowed {
		return store.AppReport{}, rate, &RateLimitedError{RetryAfter: rate.Reset, Rate: rate}
	}
	return report, rate, nil
}
