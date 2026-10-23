// Package routing distributes the queue among operators (docs/02 §9,
// docs/06 Q-01, Q-02).
//
// Phase 1 has no lines, teams, skills or shifts. A workspace either leaves
// the queue to its operators (manual) or lets the router give queued
// conversations in turn to the members available now (round_robin).
//
// Available means two things. The member accepts conversations: a status
// they choose in the panel, online or away. And the panel is open: the
// realtime gateway reports open connections every PresenceInterval
// (Seen), and a member unseen for SeenTimeout is offline. Postgres is the
// source of truth: an assignment and the turn order change in one
// transaction (docs/02 §9.1).
//
// The router runs as the job routing.dispatch, queued in the transaction
// of whatever may let it assign something: a conversation entering the
// queue (conversations calls Queued), a member coming online, the mode
// switched on.
package routing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/jobs"
	"github.com/tolkovo/tolkovo/apps/server/internal/routing/store"
)

// ErrInvalid is a mode or a status outside the allowed values.
var ErrInvalid = errors.New("routing: invalid input")

// answering are the roles that answer customers: only their members are
// available for conversations (ADR-0020: the programmer is not).
var answering = authz.RolesWith(authz.ConversationsReply)

// Mode is how a workspace distributes its queue.
type Mode string

// Modes.
const (
	ModeManual     Mode = "manual"      // operators take conversations themselves
	ModeRoundRobin Mode = "round_robin" // the router assigns them in turn
)

// Status is what a member chose: to accept conversations or not.
type Status string

// Statuses a member chooses.
const (
	StatusOnline Status = "online"
	StatusAway   Status = "away"
)

// Presence is what others see of a member.
type Presence string

// Presences: the chosen status while the panel is open, offline otherwise.
const (
	PresenceOnline  Presence = "online"
	PresenceAway    Presence = "away"
	PresenceOffline Presence = "offline"
)

// Timing of presence. The gateway reports open panels every
// PresenceInterval; a member it has not reported for SeenTimeout is
// offline: three missed reports, or a closed panel after up to 90 seconds,
// which also covers a reload or a short network loss (docs/02 §9.5).
const (
	PresenceInterval = 30 * time.Second
	SeenTimeout      = 90 * time.Second
)

// Service is the module facade.
type Service struct {
	db   *db.DB
	jobs *jobs.Client
	now  func() time.Time
}

// New returns the routing service. jobsClient queues routing.dispatch; it
// may be insert-only.
func New(d *db.DB, jobsClient *jobs.Client) *Service {
	return &Service{db: d, jobs: jobsClient, now: func() time.Time { return time.Now().UTC() }}
}

// Mode returns how the workspace distributes its queue.
func (s *Service) Mode(ctx context.Context, workspaceID uuid.UUID) (Mode, error) {
	var m Mode
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		v, err := store.New(tx).GetMode(ctx, workspaceID)
		m = Mode(v)
		return err
	})
	if err != nil {
		return "", fmt.Errorf("routing mode: %w", err)
	}
	return m, nil
}

// SetMode changes how the workspace distributes its queue. Switching to
// round_robin distributes what is waiting at once.
func (s *Service) SetMode(ctx context.Context, workspaceID uuid.UUID, mode Mode) (Mode, error) {
	if mode != ModeManual && mode != ModeRoundRobin {
		return "", fmt.Errorf("%w: mode must be %q or %q", ErrInvalid, ModeManual, ModeRoundRobin)
	}
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		if err := store.New(tx).SetMode(ctx, store.SetModeParams{WorkspaceID: workspaceID, Mode: string(mode), UpdatedAt: s.now()}); err != nil {
			return err
		}
		if mode == ModeRoundRobin {
			return s.dispatchLater(ctx, tx, workspaceID)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("set routing mode: %w", err)
	}
	return mode, nil
}

// Status returns what the member chose; online until they change it.
func (s *Service) Status(ctx context.Context, workspaceID, userID uuid.UUID) (Status, error) {
	st := StatusOnline
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		rows, err := store.New(tx).GetStates(ctx, store.GetStatesParams{WorkspaceID: workspaceID, UserIds: []uuid.UUID{userID}})
		if err == nil && len(rows) == 1 {
			st = Status(rows[0].Status)
		}
		return err
	})
	if err != nil {
		return "", fmt.Errorf("operator status: %w", err)
	}
	return st, nil
}

// SetStatus records what the member chose. Coming back online with the
// panel open lets the router give them what is waiting.
func (s *Service) SetStatus(ctx context.Context, workspaceID, userID uuid.UUID, status Status) (Status, error) {
	if status != StatusOnline && status != StatusAway {
		return "", fmt.Errorf("%w: status must be %q or %q", ErrInvalid, StatusOnline, StatusAway)
	}
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.SetStatus(ctx, store.SetStatusParams{
			WorkspaceID: workspaceID, UserID: userID, Status: string(status), UpdatedAt: s.now(),
		}); err != nil {
			return err
		}
		if status != StatusOnline {
			return nil
		}
		ok, err := q.IsAvailable(ctx, store.IsAvailableParams{Answering: answering, WorkspaceID: workspaceID, UserID: userID, SeenAfter: s.seenAfter()})
		if err != nil || !ok {
			return err
		}
		return s.dispatchIfRoundRobin(ctx, tx, workspaceID)
	})
	if err != nil {
		return "", fmt.Errorf("set operator status: %w", err)
	}
	return status, nil
}

// Presences returns what others see of the workspace's members; members
// missing from the map are offline.
func (s *Service) Presences(ctx context.Context, workspaceID uuid.UUID) (map[uuid.UUID]Presence, error) {
	out := map[uuid.UUID]Presence{}
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		rows, err := store.New(tx).ListStates(ctx, workspaceID)
		for _, r := range rows {
			out[r.UserID] = presence(Status(r.Status), r.SeenAt, s.seenAfter())
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("presences: %w", err)
	}
	return out, nil
}

// Seen records that userIDs have the panel open now; the realtime gateway
// calls it when a panel connects and every PresenceInterval for each
// workspace with open panels. A member who was offline and accepts
// conversations lets the router give them what is waiting.
func (s *Service) Seen(ctx context.Context, workspaceID uuid.UUID, userIDs []uuid.UUID) error {
	if len(userIDs) == 0 {
		return nil
	}
	now := s.now()
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		before, err := q.GetStates(ctx, store.GetStatesParams{WorkspaceID: workspaceID, UserIds: userIDs})
		if err != nil {
			return err
		}
		if err := q.MarkSeen(ctx, store.MarkSeenParams{SeenAt: now, WorkspaceID: workspaceID, UserIds: userIDs}); err != nil {
			return err
		}
		known := make(map[uuid.UUID]bool, len(before))
		back := false
		for _, r := range before {
			known[r.UserID] = true
			if Status(r.Status) == StatusOnline && presence(StatusOnline, r.SeenAt, s.seenAfter()) == PresenceOffline {
				back = true
			}
		}
		// A member seen for the first time accepts conversations by default.
		back = back || len(known) < len(userIDs)
		if !back {
			return nil
		}
		return s.dispatchIfRoundRobin(ctx, tx, workspaceID)
	})
	if err != nil {
		return fmt.Errorf("operators seen: %w", err)
	}
	return nil
}

// Queued implements conversations.Router: a conversation entered the queue
// in tx. The member who gave it back goes to the end of the turn order, so
// the router does not hand it straight back to them.
func (s *Service) Queued(ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID, releasedBy *uuid.UUID) error {
	q := store.New(tx)
	mode, err := q.GetMode(ctx, workspaceID)
	if err != nil || Mode(mode) != ModeRoundRobin {
		return err
	}
	if releasedBy != nil {
		if err := q.SetLastAssigned(ctx, store.SetLastAssignedParams{
			AssignedAt: s.now(), WorkspaceID: workspaceID, UserID: *releasedBy,
		}); err != nil {
			return err
		}
	}
	return s.dispatchLater(ctx, tx, workspaceID)
}

// Available implements conversations.Router: with automatic distribution a
// conversation the customer reopens goes back to its operator only while
// they accept conversations and have the panel open; otherwise it goes to
// the queue and the router finds someone who does. With manual
// distribution the operator keeps it.
func (s *Service) Available(ctx context.Context, tx pgx.Tx, workspaceID, userID uuid.UUID) (bool, error) {
	q := store.New(tx)
	mode, err := q.GetMode(ctx, workspaceID)
	if err != nil || Mode(mode) != ModeRoundRobin {
		return true, err
	}
	return q.IsAvailable(ctx, store.IsAvailableParams{Answering: answering, WorkspaceID: workspaceID, UserID: userID, SeenAfter: s.seenAfter()})
}

// AnyOnline reports whether someone in the workspace takes conversations
// now: online and with the panel open, in either mode. The widget offers
// its offline form without them (W-02).
func (s *Service) AnyOnline(ctx context.Context, workspaceID uuid.UUID) (bool, error) {
	var online bool
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		online, err = store.New(tx).AnyAvailable(ctx, store.AnyAvailableParams{Answering: answering, WorkspaceID: workspaceID, SeenAfter: s.seenAfter()})
		return err
	})
	if err != nil {
		return false, fmt.Errorf("routing: any online: %w", err)
	}
	return online, nil
}

func (s *Service) seenAfter() time.Time { return s.now().Add(-SeenTimeout) }

func (s *Service) dispatchIfRoundRobin(ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID) error {
	mode, err := store.New(tx).GetMode(ctx, workspaceID)
	if err != nil || Mode(mode) != ModeRoundRobin {
		return err
	}
	return s.dispatchLater(ctx, tx, workspaceID)
}

func presence(st Status, seenAt *time.Time, seenAfter time.Time) Presence {
	if seenAt == nil || !seenAt.After(seenAfter) {
		return PresenceOffline
	}
	if st == StatusAway {
		return PresenceAway
	}
	return PresenceOnline
}
