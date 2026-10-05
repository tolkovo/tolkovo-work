package integrations

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/integrations/github"
	"github.com/tolkovo/tolkovo/apps/server/internal/integrations/store"
)

// The App's webhook (GH-03, ADR-0012). GitHub posts to HookPath; the
// signature is checked against the integration's sealed webhook secret
// before the body is read as JSON. A delivery is applied in one tenant
// transaction together with its X-GitHub-Delivery id, so a repeat does
// nothing and a failure leaves nothing behind: GitHub does not retry by
// itself, the delivery can be redelivered from the App's settings.
//
// "issues": closed or reopened issues update every conversation linked to
// them. "installation" deleted: the App was uninstalled, the integration
// waits to be installed again. Everything else is acknowledged and ignored.

// ErrHookSignature means the webhook's signature does not match the secret.
var ErrHookSignature = errors.New("integrations: the webhook signature does not match")

const (
	// maxDeliveryID bounds X-GitHub-Delivery, a GUID.
	maxDeliveryID = 100
	// deliveryKeep is how long delivery ids are remembered; GitHub
	// redelivers up to three days back.
	deliveryKeep = 7 * 24 * time.Hour
)

// Hook is one webhook delivery as GitHub sent it.
type Hook struct {
	// Event is X-GitHub-Event: "issues", "installation", "ping"…
	Event string
	// Delivery is X-GitHub-Delivery, unique per delivery.
	Delivery string
	// Signature is X-Hub-Signature-256.
	Signature string
	Body      []byte
}

// GitHubHook applies one webhook delivery. An integration that is not
// there, disabled or without its secret is ErrNotFound; a wrong signature
// is ErrHookSignature, decided before anything else is read.
func (s *Service) GitHubHook(ctx context.Context, workspaceID, integrationID uuid.UUID, h Hook) error {
	integ, sec, err := s.hookIntegration(ctx, workspaceID, integrationID)
	if err != nil {
		return err
	}
	if !github.VerifySignature([]byte(sec.WebhookSecret), h.Signature, h.Body) {
		return ErrHookSignature
	}
	if h.Event != "issues" && h.Event != "installation" {
		return nil // ping and events the App was not asked for
	}
	if h.Delivery == "" || len(h.Delivery) > maxDeliveryID {
		return fmt.Errorf("%w: X-GitHub-Delivery is required", ErrInvalid)
	}
	ev, err := github.ParseHookEvent(h.Body)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	now := s.now()
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		fresh, err := q.InsertDelivery(ctx, store.InsertDeliveryParams{
			WorkspaceID: workspaceID, IntegrationID: integ.ID, DeliveryID: h.Delivery, ReceivedAt: now,
		})
		if err != nil || len(fresh) == 0 {
			return err // a repeat: applied before
		}
		if err := q.PruneDeliveries(ctx, store.PruneDeliveriesParams{
			WorkspaceID: workspaceID, IntegrationID: integ.ID, Before: now.Add(-deliveryKeep),
		}); err != nil {
			return err
		}
		if h.Event == "installation" {
			return s.installationEvent(ctx, q, integ, ev, now)
		}
		return s.issuesEvent(ctx, tx, q, integ, ev, h.Delivery, now)
	})
	if err != nil {
		return fmt.Errorf("github webhook: %w", err)
	}
	return nil
}

// hookIntegration loads the integration a webhook is for and opens its
// secret. A disconnected one has no secret left.
func (s *Service) hookIntegration(ctx context.Context, workspaceID, integrationID uuid.UUID) (store.Integration, appSecret, error) {
	if s.opts.Sealer == nil {
		return store.Integration{}, appSecret{}, ErrNotFound
	}
	var row store.Integration
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		row, err = store.New(tx).GetIntegration(ctx, store.GetIntegrationParams{WorkspaceID: workspaceID, ID: integrationID})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.Status == StatusDisabled) {
		return store.Integration{}, appSecret{}, ErrNotFound
	}
	if err != nil {
		return store.Integration{}, appSecret{}, fmt.Errorf("load github integration: %w", err)
	}
	sec, err := s.secret(ctx, row)
	if errors.Is(err, ErrStep) || (err == nil && sec.WebhookSecret == "") {
		return store.Integration{}, appSecret{}, ErrNotFound
	}
	if err != nil {
		return store.Integration{}, appSecret{}, err
	}
	return row, sec, nil
}

// installationEvent: the App was uninstalled from the account, so issues
// cannot be created until it is installed again. The App, its key and the
// issues linked so far stay.
func (s *Service) installationEvent(ctx context.Context, q *store.Queries, integ store.Integration, ev github.HookEvent, now time.Time) error {
	if ev.Action != "deleted" || ev.Installation == nil {
		return nil
	}
	_, err := q.DetachInstallation(ctx, store.DetachInstallationParams{
		UpdatedAt: now, WorkspaceID: integ.WorkspaceID, ID: integ.ID, InstallationID: &ev.Installation.ID,
	})
	return err
}

// issuesEvent applies a closed or reopened issue to every conversation
// linked to it. A link already in that state is left alone, so events
// that come out of order do not flip it back and forth.
func (s *Service) issuesEvent(
	ctx context.Context, tx pgx.Tx, q *store.Queries, integ store.Integration, ev github.HookEvent, delivery string, now time.Time,
) error {
	if ev.Issue == nil || ev.Repository == nil || ev.Issue.PullRequest != nil {
		return nil
	}
	var to, reason string
	switch ev.Action {
	case "closed":
		to, reason = IssueClosed, conversations.IssueFixed
		if r := ev.Issue.StateReason; r != nil && (*r == "not_planned" || *r == "duplicate") {
			reason = conversations.IssueNotPlanned
		}
	case "reopened":
		to = IssueOpen
	default:
		return nil
	}
	if s.opts.Conversations == nil {
		return errors.New("integrations: no conversations to record issues in")
	}
	links, err := q.IssueLinksByNumberForUpdate(ctx, store.IssueLinksByNumberForUpdateParams{
		WorkspaceID: integ.WorkspaceID, IntegrationID: integ.ID, RepoID: ev.Repository.ID, Number: &ev.Issue.Number,
	})
	if err != nil {
		return err
	}
	for _, link := range links {
		if link.Status == to {
			continue
		}
		p := store.SetIssueStateParams{Status: to, UpdatedAt: now, WorkspaceID: link.WorkspaceID, ID: link.ID}
		if to == IssueClosed {
			p.CloseReason, p.ClosedAt = &reason, &now
		}
		row, err := q.SetIssueState(ctx, p)
		if err != nil {
			return err
		}
		other, err := q.HasOtherOpenIssue(ctx, store.HasOtherOpenIssueParams{
			WorkspaceID: link.WorkspaceID, ConversationID: link.ConversationID, ExceptID: link.ID,
		})
		if err != nil {
			return err
		}
		note := conversations.IssueNote{Type: conversations.EventIssueReopened, Issue: issueRef(row), AwaitingFix: true, Key: delivery}
		if to == IssueClosed {
			note.Type, note.Issue.Reason, note.AwaitingFix = conversations.EventIssueClosed, reason, other
		}
		if err := s.opts.Conversations.RecordIssue(ctx, tx, link.WorkspaceID, link.ConversationID, note); err != nil {
			return err
		}
		if err := issuesChanged(ctx, tx, link.WorkspaceID, link.ConversationID); err != nil {
			return err
		}
	}
	return nil
}

// ReplyDraft is the reply about a fix the operator may send (GH-03).
type ReplyDraft struct {
	LinkID uuid.UUID
	Number int64
	Text   string
}

// replyDraft drafts the reply about the newest issue of the conversation
// closed as fixed, from the reply template in the contact's language
// (locale, the workspace's, when unknown). There is none when no issue was
// fixed, or an operator has written to the customer since.
func (s *Service) replyDraft(ctx context.Context, workspaceID, conversationID uuid.UUID, integ *store.Integration, fixed store.IssueLink, locale string) (*ReplyDraft, error) {
	src, err := s.opts.Conversations.ReplySource(ctx, workspaceID, conversationID)
	if err != nil {
		return nil, err
	}
	if fixed.ClosedAt == nil || fixed.Number == nil || !fixed.ClosedAt.After(src.LastReplyAt) {
		return nil, nil
	}
	if src.ContactLocale != "" {
		locale = src.ContactLocale
	}
	tpl := DefaultTemplates(locale)
	if integ != nil {
		tpl = s.integrationFrom(*integ, locale).Templates
	}
	text := tpl.ReplyRU
	if locale == "en" {
		text = tpl.ReplyEN
	}
	text = Render(text, map[string]string{"name": src.ContactName, "issue_number": fmt.Sprint(*fixed.Number)})
	return &ReplyDraft{LinkID: fixed.ID, Number: *fixed.Number, Text: truncate(text, maxReply)}, nil
}
