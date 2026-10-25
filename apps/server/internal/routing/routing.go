// Package routing distributes the queue among operators (docs/02 §9,
// docs/06 Q-01, Q-02, ADR-0021).
//
// There are no teams, skills or shifts yet. A workspace either leaves the
// queue to its operators (manual) or lets the router give queued
// conversations to the members available now: in turn (round_robin) or to
// the least loaded (least_loaded), each up to their capacity (max_chats).
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
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/jobs"
	"github.com/tolkovo/tolkovo/apps/server/internal/routing/store"
)

// Errors.
var (
	// ErrInvalid is a mode, a status or a limit outside the allowed values.
	ErrInvalid = errors.New("routing: invalid input")
	// ErrNotFound is a member who is not an active one answering customers.
	ErrNotFound = errors.New("routing: not found")
)

// MaxChatsLimit is the highest capacity one may set.
const MaxChatsLimit = 50

// answering are the roles that answer customers: only their members are
// available for conversations (ADR-0020: the programmer is not).
var answering = authz.RolesWith(authz.ConversationsReply)

// Mode is how a workspace distributes its queue.
type Mode string

// Modes.
const (
	ModeManual      Mode = "manual"       // operators take conversations themselves
	ModeRoundRobin  Mode = "round_robin"  // the router assigns them in turn
	ModeLeastLoaded Mode = "least_loaded" // the router assigns them to the least loaded
)

// auto: the router assigns conversations itself.
func auto(m string) bool { return Mode(m) == ModeRoundRobin || Mode(m) == ModeLeastLoaded }

// Settings are how a workspace distributes its queue.
type Settings struct {
	Mode Mode
	// MaxChats is how many conversations in work the router gives a
	// member; nil is no limit. A member's own limit overrides it.
	MaxChats *int
}

// Limit is a member's own capacity and their load now.
type Limit struct {
	UserID uuid.UUID
	// MaxChats is the member's own limit; nil follows the workspace's.
	MaxChats *int
	// Open is how many conversations they have in work.
	Open int
}

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

// Settings returns how the workspace distributes its queue.
func (s *Service) Settings(ctx context.Context, workspaceID uuid.UUID) (Settings, error) {
	var out Settings
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		row, err := store.New(tx).GetSettings(ctx, workspaceID)
		out = Settings{Mode: Mode(row.Mode), MaxChats: intOf(row.MaxChats)}
		return err
	})
	if err != nil {
		return Settings{}, fmt.Errorf("routing settings: %w", err)
	}
	return out, nil
}

// SetSettings changes how the workspace distributes its queue. Switching to
// automatic distribution, or giving members more room, distributes what is
// waiting at once.
func (s *Service) SetSettings(ctx context.Context, workspaceID uuid.UUID, in Settings) (Settings, error) {
	if in.Mode != ModeManual && in.Mode != ModeRoundRobin && in.Mode != ModeLeastLoaded {
		return Settings{}, fmt.Errorf("%w: mode must be %q, %q or %q", ErrInvalid, ModeManual, ModeRoundRobin, ModeLeastLoaded)
	}
	limit, err := limitOf(in.MaxChats)
	if err != nil {
		return Settings{}, err
	}
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		if err := store.New(tx).SetSettings(ctx, store.SetSettingsParams{
			WorkspaceID: workspaceID, Mode: string(in.Mode), MaxChats: limit, UpdatedAt: s.now(),
		}); err != nil {
			return err
		}
		if auto(string(in.Mode)) {
			return s.dispatchLater(ctx, tx, workspaceID)
		}
		return nil
	})
	if err != nil {
		return Settings{}, fmt.Errorf("set routing settings: %w", err)
	}
	return in, nil
}

// Limits returns the own limit of each active member answering customers
// and their load now. load counts conversations in work; the conversations
// module provides it.
func (s *Service) Limits(
	ctx context.Context, workspaceID uuid.UUID,
	load func(ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID, userIDs []uuid.UUID) (map[uuid.UUID]int, error),
) ([]Limit, error) {
	var out []Limit
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		rows, err := store.New(tx).ListLimits(ctx, store.ListLimitsParams{WorkspaceID: workspaceID, Answering: answering})
		if err != nil {
			return err
		}
		ids := make([]uuid.UUID, len(rows))
		for i, r := range rows {
			ids[i] = r.UserID
		}
		open, err := load(ctx, tx, workspaceID, ids)
		if err != nil {
			return err
		}
		out = make([]Limit, len(rows))
		for i, r := range rows {
			out[i] = Limit{UserID: r.UserID, MaxChats: intOf(r.MaxChats), Open: open[r.UserID]}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("routing limits: %w", err)
	}
	return out, nil
}

// SetLimit sets a member's own capacity; nil follows the workspace's. A
// member given more room may get what is waiting.
func (s *Service) SetLimit(ctx context.Context, workspaceID, userID uuid.UUID, maxChats *int) error {
	limit, err := limitOf(maxChats)
	if err != nil {
		return err
	}
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		n, err := store.New(tx).SetLimit(ctx, store.SetLimitParams{
			MaxChats: limit, UpdatedAt: s.now(), WorkspaceID: workspaceID, UserID: userID, Answering: answering,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return s.dispatchIfAuto(ctx, tx, workspaceID)
	})
	if err != nil {
		return fmt.Errorf("set routing limit: %w", err)
	}
	return nil
}

func limitOf(n *int) (pgtype.Int2, error) {
	if n == nil {
		return pgtype.Int2{}, nil
	}
	if *n < 1 || *n > MaxChatsLimit {
		return pgtype.Int2{}, fmt.Errorf("%w: max_chats must be between 1 and %d", ErrInvalid, MaxChatsLimit)
	}
	return pgtype.Int2{Int16: int16(*n), Valid: true}, nil //nolint:gosec // checked above
}

func intOf(n pgtype.Int2) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int16)
	return &v
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
		return s.dispatchIfAuto(ctx, tx, workspaceID)
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
		return s.dispatchIfAuto(ctx, tx, workspaceID)
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
	if err != nil || !auto(mode) {
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
	if err != nil || !auto(mode) {
		return true, err
	}
	return q.IsAvailable(ctx, store.IsAvailableParams{Answering: answering, WorkspaceID: workspaceID, UserID: userID, SeenAfter: s.seenAfter()})
}

// Freed implements conversations.Router: a conversation left the work of a
// member. With automatic distribution and capacity limits in use they may
// have room now, so the router looks at the queue again; without limits
// nobody waited for room.
func (s *Service) Freed(ctx context.Context, tx pgx.Tx, workspaceID, _ uuid.UUID) error {
	q := store.New(tx)
	set, err := q.GetSettings(ctx, workspaceID)
	if err != nil || !auto(set.Mode) {
		return err
	}
	if !set.MaxChats.Valid {
		own, err := q.AnyOwnLimit(ctx, workspaceID)
		if err != nil || !own {
			return err
		}
	}
	return s.dispatchLater(ctx, tx, workspaceID)
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

func (s *Service) dispatchIfAuto(ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID) error {
	mode, err := store.New(tx).GetMode(ctx, workspaceID)
	if err != nil || !auto(mode) {
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
