// Package tenancy manages workspaces and their members. It is the only module
// that writes the workspaces, roles and memberships tables.
package tenancy

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy/store"
)

// Domain errors. The api layer maps them to problem+json codes.
var (
	ErrNotFound      = errors.New("tenancy: not found")
	ErrInvalid       = errors.New("tenancy: invalid input")
	ErrSlugTaken     = errors.New("tenancy: slug is taken")
	ErrEmailTaken    = errors.New("tenancy: email is taken")
	ErrAlreadyMember = errors.New("tenancy: user is already a member")
	ErrUnknownRole   = errors.New("tenancy: unknown role")
)

// System role keys available in phase 1. They are the keys of the RBAC
// matrix in platform/authz.
const (
	RoleOwner    = authz.RoleOwner
	RoleAdmin    = authz.RoleAdmin
	RoleOperator = authz.RoleOperator
)

const pgUniqueViolation = "23505"

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`)

// Workspace is one tenant.
type Workspace struct {
	ID        uuid.UUID
	Slug      string
	Name      string
	Locale    string
	Timezone  string
	CreatedAt time.Time
}

// Member is a user inside a workspace.
type Member struct {
	UserID             uuid.UUID
	Email              string
	Name               string
	Role               string
	MaxConcurrentChats int
	LineLevel          int
	Active             bool
}

// CreateWorkspaceInput describes a new workspace and its first owner.
type CreateWorkspaceInput struct {
	Name     string
	Slug     string
	Locale   string // "ru" or "en"; defaults to "ru"
	Timezone string // IANA name; defaults to "Europe/Moscow"
	OwnerID  uuid.UUID
}

// Service is the module facade.
type Service struct {
	db *db.DB
}

// New returns the tenancy service.
func New(d *db.DB) *Service { return &Service{db: d} }

// CreateWorkspace creates a workspace and makes OwnerID its owner in one
// transaction.
func (s *Service) CreateWorkspace(ctx context.Context, in CreateWorkspaceInput) (Workspace, error) {
	if err := in.normalize(); err != nil {
		return Workspace{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Workspace{}, fmt.Errorf("new workspace id: %w", err)
	}
	var ws Workspace
	err = s.db.InTenantTx(ctx, id, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := q.CreateWorkspace(ctx, store.CreateWorkspaceParams{
			ID: id, Slug: in.Slug, Name: in.Name, Locale: in.Locale, Timezone: in.Timezone,
		})
		if err != nil {
			return err
		}
		ws = Workspace(row)
		return addMember(ctx, q, id, in.OwnerID, RoleOwner)
	})
	if err != nil {
		if isUnique(err, "workspaces_slug_key") {
			return Workspace{}, ErrSlugTaken
		}
		return Workspace{}, fmt.Errorf("create workspace: %w", err)
	}
	return ws, nil
}

// Workspace returns a workspace by ID. A workspace the caller cannot see
// yields ErrNotFound, never a permission error.
func (s *Service) Workspace(ctx context.Context, workspaceID uuid.UUID) (Workspace, error) {
	var ws Workspace
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		row, err := store.New(tx).GetWorkspace(ctx, workspaceID)
		ws = Workspace(row)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Workspace{}, ErrNotFound
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("get workspace %s: %w", workspaceID, err)
	}
	return ws, nil
}

// AddMember adds an existing user to the workspace with a role.
func (s *Service) AddMember(ctx context.Context, workspaceID, userID uuid.UUID, role string) error {
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		return addMember(ctx, store.New(tx), workspaceID, userID, role)
	})
	if err != nil && !errors.Is(err, ErrUnknownRole) && !errors.Is(err, ErrAlreadyMember) {
		return fmt.Errorf("add member to %s: %w", workspaceID, err)
	}
	return err
}

// Members lists the members of a workspace.
func (s *Service) Members(ctx context.Context, workspaceID uuid.UUID) ([]Member, error) {
	var members []Member
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		rows, err := store.New(tx).ListMembers(ctx, workspaceID)
		if err != nil {
			return err
		}
		members = make([]Member, 0, len(rows))
		for _, r := range rows {
			members = append(members, Member{
				UserID: r.UserID, Email: r.Email, Name: r.Name, Role: r.RoleKey,
				MaxConcurrentChats: int(r.MaxConcurrentChats), LineLevel: int(r.LineLevel), Active: r.IsActive,
			})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list members of %s: %w", workspaceID, err)
	}
	return members, nil
}

func addMember(ctx context.Context, q *store.Queries, workspaceID, userID uuid.UUID, role string) error {
	n, err := q.AddMember(ctx, store.AddMemberParams{WorkspaceID: workspaceID, UserID: userID, RoleKey: role})
	switch {
	case isUnique(err, "memberships_pkey"):
		return ErrAlreadyMember
	case err != nil:
		return err
	case n == 0:
		return fmt.Errorf("%w: %q", ErrUnknownRole, role)
	}
	return nil
}

func (in *CreateWorkspaceInput) normalize() error {
	if in.Locale == "" {
		in.Locale = "ru"
	}
	if in.Timezone == "" {
		in.Timezone = "Europe/Moscow"
	}
	switch {
	case in.OwnerID == uuid.Nil:
		return fmt.Errorf("%w: owner is required", ErrInvalid)
	case !slugRe.MatchString(in.Slug):
		return fmt.Errorf("%w: slug must be 3-40 chars of a-z, 0-9 and inner hyphens", ErrInvalid)
	case in.Name == "" || utf8.RuneCountInString(in.Name) > 120:
		return fmt.Errorf("%w: name must be 1-120 characters", ErrInvalid)
	case in.Locale != "ru" && in.Locale != "en":
		return fmt.Errorf("%w: locale must be ru or en", ErrInvalid)
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return fmt.Errorf("%w: unknown timezone %q", ErrInvalid, in.Timezone)
	}
	return nil
}

func isUnique(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation && pgErr.ConstraintName == constraint
}
