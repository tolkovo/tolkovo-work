// Package notifications reminds operators about conversations that need
// them (docs/04 §5.5, docs/06 I-05). Sounds and desktop notifications live
// in the panel and stay settings of a browser; the server keeps what it
// acts on itself: the email about customer messages a member left unread.
//
// The conversations module calls NeedsAttention inside the transaction of
// the change (a conversation given to a member). It queues one delayed job
// per member and conversation; the job emails only if the conversation is
// still the member's and still has messages they have not read.
//
// Messages about tasks go to members' Telegram through the workspace's
// notifications bot (TK-02, telegram.go).
package notifications

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/tolkovo/tolkovo/apps/server/internal/notifications/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/jobs"
)

// ErrInvalid is a setting outside the allowed values.
var ErrInvalid = errors.New("notifications: invalid input")

// EmailDelays are the allowed waits, in minutes, before the email about
// unread messages.
var EmailDelays = []int{5, 15, 30, 60}

// Defaults of a member who never changed the settings (migration 00009).
const (
	DefaultEmailMissed = true
	DefaultEmailDelay  = 15
)

// Prefs are a member's notification settings in one workspace.
type Prefs struct {
	// EmailMissed sends an email when a conversation of the member keeps
	// unread customer messages for EmailDelay minutes.
	EmailMissed bool
	EmailDelay  int
}

// PrefsPatch changes the fields that are set.
type PrefsPatch struct {
	EmailMissed *bool
	EmailDelay  *int
}

// Service is the module facade.
type Service struct {
	db *db.DB
	// jobs queues the emails; nil when the server sends no email, and then
	// NeedsAttention does nothing.
	jobs *jobs.Client
	// tg connects Telegram (TK-02); zero sends nothing there.
	tg  TelegramOptions
	now func() time.Time
}

// New returns the notifications service. jobs may be nil: the server has
// no outgoing email, the settings still work.
func New(d *db.DB, jobsClient *jobs.Client) *Service {
	return &Service{db: d, jobs: jobsClient, now: func() time.Time { return time.Now().UTC() }}
}

// EmailEnabled reports whether the server sends the emails at all.
func (s *Service) EmailEnabled() bool { return s.jobs != nil }

// Prefs returns the member's settings, defaults when never changed.
func (s *Service) Prefs(ctx context.Context, workspaceID, userID uuid.UUID) (Prefs, error) {
	var p Prefs
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		p, err = prefsIn(ctx, store.New(tx), workspaceID, userID)
		return err
	})
	if err != nil {
		return Prefs{}, fmt.Errorf("notification prefs: %w", err)
	}
	return p, nil
}

// UpdatePrefs changes the member's settings and returns them.
func (s *Service) UpdatePrefs(ctx context.Context, workspaceID, userID uuid.UUID, in PrefsPatch) (Prefs, error) {
	params := store.UpsertPrefsParams{WorkspaceID: workspaceID, UserID: userID, UpdatedAt: s.now()}
	if in.EmailMissed != nil {
		params.EmailMissed = pgtype.Bool{Bool: *in.EmailMissed, Valid: true}
	}
	if in.EmailDelay != nil {
		if !slices.Contains(EmailDelays, *in.EmailDelay) {
			return Prefs{}, fmt.Errorf("%w: email delay must be one of %v minutes", ErrInvalid, EmailDelays)
		}
		params.EmailDelayMinutes = pgtype.Int2{Int16: int16(*in.EmailDelay), Valid: true} //nolint:gosec // G115: one of EmailDelays
	}
	var p Prefs
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		row, err := store.New(tx).UpsertPrefs(ctx, params)
		p = Prefs{EmailMissed: row.EmailMissed, EmailDelay: int(row.EmailDelayMinutes)}
		return err
	})
	if err != nil {
		return Prefs{}, fmt.Errorf("update notification prefs: %w", err)
	}
	return p, nil
}

func prefsIn(ctx context.Context, q *store.Queries, workspaceID, userID uuid.UUID) (Prefs, error) {
	row, err := q.GetPrefs(ctx, store.GetPrefsParams{WorkspaceID: workspaceID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Prefs{EmailMissed: DefaultEmailMissed, EmailDelay: DefaultEmailDelay}, nil
	}
	if err != nil {
		return Prefs{}, err
	}
	return Prefs{EmailMissed: row.EmailMissed, EmailDelay: int(row.EmailDelayMinutes)}, nil
}

// NeedsAttention queues the email about the conversation for the member,
// due after their delay. It implements conversations.Notifier: tx is the
// tenant transaction of the change. A job already waiting for the same
// member and conversation absorbs the new one, so a busy conversation
// gives one email, at the earliest deadline.
func (s *Service) NeedsAttention(ctx context.Context, tx pgx.Tx, workspaceID, conversationID, userID uuid.UUID) error {
	if s.jobs == nil {
		return nil
	}
	p, err := prefsIn(ctx, store.New(tx), workspaceID, userID)
	if err != nil {
		return fmt.Errorf("notification prefs: %w", err)
	}
	if !p.EmailMissed {
		return nil
	}
	_, err = s.jobs.InsertTx(ctx, tx, MissedEmailArgs{
		WorkspaceID: workspaceID, ConversationID: conversationID, UserID: userID,
	}, &river.InsertOpts{
		ScheduledAt: s.now().Add(time.Duration(p.EmailDelay) * time.Minute),
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			// Only waiting jobs: after one ran, new messages may need a new email.
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning, rivertype.JobStateScheduled,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("queue missed email: %w", err)
	}
	return nil
}
