package tenancy

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy/store"
)

// User is a global account; one user can belong to several workspaces.
type User struct {
	ID     uuid.UUID
	Email  string
	Name   string
	Locale string
	// EmailVerifiedAt is when the user proved they own the address: an
	// accepted invitation, a password reset or the confirmation link.
	EmailVerifiedAt *time.Time
}

// Credentials are what login needs to check a password. PasswordHash is a
// PHC string and must never be logged or returned by the API.
type Credentials struct {
	UserID       uuid.UUID
	PasswordHash string
	Disabled     bool
}

// Membership is a user inside one workspace with a role.
type Membership struct {
	User      User
	Workspace Workspace
	Role      string
}

// WorkspaceSummary is one entry of the user's workspace list.
type WorkspaceSummary struct {
	ID   uuid.UUID
	Slug string
	Name string
	Role string
}

// RegisterInput describes a new account and its first workspace.
type RegisterInput struct {
	Email         string
	Name          string
	Locale        string // "ru" or "en"; defaults to "ru"; also the workspace locale
	PasswordHash  string // produced by the auth module, never a plain password
	WorkspaceName string
	WorkspaceSlug string
	Timezone      string
	// AfterCreate runs in the same transaction once the account exists; the
	// auth module queues the confirmation email here (ADR-0004).
	AfterCreate func(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error
}

// UpdateWorkspaceInput changes workspace settings; nil fields stay as they are.
type UpdateWorkspaceInput struct {
	Name     *string
	Locale   *string
	Timezone *string
}

// NormalizeEmail trims the address and checks that it is a bare address.
// Case is kept: users.email is citext, so lookups ignore case anyway.
func NormalizeEmail(email string) (string, error) {
	email = strings.TrimSpace(email)
	if len(email) < 3 || len(email) > 254 {
		return "", fmt.Errorf("%w: email must be 3-254 characters", ErrInvalid)
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Name != "" || addr.Address != email {
		return "", fmt.Errorf("%w: email is not a valid address", ErrInvalid)
	}
	return email, nil
}

// Register creates the user, the workspace and the owner membership in one
// transaction (docs/06 A-01). users has no RLS, so the global row is written
// inside the tenant transaction of the new workspace: nothing is left behind
// if any step fails, and no SECURITY DEFINER code is needed.
func (s *Service) Register(ctx context.Context, in RegisterInput) (Membership, error) {
	email, err := NormalizeEmail(in.Email)
	if err != nil {
		return Membership{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 120 {
		return Membership{}, fmt.Errorf("%w: name must be 1-120 characters", ErrInvalid)
	}
	if in.PasswordHash == "" {
		return Membership{}, fmt.Errorf("%w: password hash is required", ErrInvalid)
	}
	userID, err := uuid.NewV7()
	if err != nil {
		return Membership{}, fmt.Errorf("new user id: %w", err)
	}
	wsIn := CreateWorkspaceInput{
		Name: strings.TrimSpace(in.WorkspaceName), Slug: in.WorkspaceSlug,
		Locale: in.Locale, Timezone: in.Timezone, OwnerID: userID,
	}
	if err := wsIn.normalize(); err != nil {
		return Membership{}, err
	}
	wsID, err := uuid.NewV7()
	if err != nil {
		return Membership{}, fmt.Errorf("new workspace id: %w", err)
	}

	var m Membership
	err = s.db.InTenantTx(ctx, wsID, func(tx pgx.Tx) error {
		q := store.New(tx)
		u, err := q.CreateUser(ctx, store.CreateUserParams{
			ID: userID, Email: email, Name: in.Name, Locale: wsIn.Locale,
			PasswordHash: pgtype.Text{String: in.PasswordHash, Valid: true},
		})
		if err != nil {
			return err
		}
		ws, err := q.CreateWorkspace(ctx, store.CreateWorkspaceParams{
			ID: wsID, Slug: wsIn.Slug, Name: wsIn.Name, Locale: wsIn.Locale, Timezone: wsIn.Timezone,
		})
		if err != nil {
			return err
		}
		m = Membership{User: User(u), Workspace: Workspace(ws), Role: RoleOwner}
		if err := addMember(ctx, q, wsID, userID, RoleOwner); err != nil {
			return err
		}
		if in.AfterCreate != nil {
			return in.AfterCreate(ctx, tx, userID)
		}
		return nil
	})
	switch {
	case isUnique(err, "users_email_key"):
		return Membership{}, ErrEmailTaken
	case isUnique(err, "workspaces_slug_key"):
		return Membership{}, ErrSlugTaken
	case err != nil:
		return Membership{}, fmt.Errorf("register: %w", err)
	}
	return m, nil
}

// Credentials returns the password hash of the account with this email, or
// ErrNotFound. Accounts without a password (future SSO) are ErrNotFound too.
func (s *Service) Credentials(ctx context.Context, email string) (Credentials, error) {
	var c Credentials
	err := s.db.InGlobalTx(ctx, func(tx pgx.Tx) error {
		row, err := store.New(tx).GetUserCredentials(ctx, strings.TrimSpace(email))
		if err != nil {
			return err
		}
		if !row.PasswordHash.Valid {
			return pgx.ErrNoRows
		}
		c = Credentials{UserID: row.ID, PasswordHash: row.PasswordHash.String, Disabled: row.DisabledAt != nil}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Credentials{}, ErrNotFound
	}
	if err != nil {
		return Credentials{}, fmt.Errorf("get credentials: %w", err)
	}
	return c, nil
}

// CredentialsByID returns the password hash of the account, or ErrNotFound
// when there is no such account or it has no password.
func (s *Service) CredentialsByID(ctx context.Context, userID uuid.UUID) (Credentials, error) {
	var c Credentials
	err := s.db.InGlobalTx(ctx, func(tx pgx.Tx) error {
		row, err := store.New(tx).GetUserPasswordHash(ctx, userID)
		if err != nil {
			return err
		}
		if !row.PasswordHash.Valid {
			return pgx.ErrNoRows
		}
		c = Credentials{UserID: row.ID, PasswordHash: row.PasswordHash.String, Disabled: row.DisabledAt != nil}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Credentials{}, ErrNotFound
	}
	if err != nil {
		return Credentials{}, fmt.Errorf("get credentials of %s: %w", userID, err)
	}
	return c, nil
}

// SetPasswordHash replaces the password of an enabled account. hash is a
// PHC string produced by the auth module, never a plain password.
func (s *Service) SetPasswordHash(ctx context.Context, userID uuid.UUID, hash string) error {
	if hash == "" {
		return fmt.Errorf("%w: password hash is required", ErrInvalid)
	}
	var n int64
	err := s.db.InGlobalTx(ctx, func(tx pgx.Tx) error {
		var err error
		n, err = store.New(tx).SetUserPasswordHash(ctx, store.SetUserPasswordHashParams{
			ID: userID, PasswordHash: pgtype.Text{String: hash, Valid: true},
		})
		return err
	})
	if err != nil {
		return fmt.Errorf("set password of %s: %w", userID, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateUserInput changes the profile of an account; nil fields stay as
// they are.
type UpdateUserInput struct {
	Name   *string
	Locale *string
}

// UpdateUser changes the name and interface language of an enabled
// account. users is global, so the change applies in every workspace.
func (s *Service) UpdateUser(ctx context.Context, userID uuid.UUID, in UpdateUserInput) (User, error) {
	var name, locale pgtype.Text
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" || utf8.RuneCountInString(n) > 120 {
			return User{}, fmt.Errorf("%w: name must be 1-120 characters", ErrInvalid)
		}
		name = pgtype.Text{String: n, Valid: true}
	}
	if in.Locale != nil {
		if *in.Locale != "ru" && *in.Locale != "en" {
			return User{}, fmt.Errorf("%w: locale must be ru or en", ErrInvalid)
		}
		locale = pgtype.Text{String: *in.Locale, Valid: true}
	}
	var u User
	err := s.db.InGlobalTx(ctx, func(tx pgx.Tx) error {
		row, err := store.New(tx).UpdateUserProfile(ctx, store.UpdateUserProfileParams{ID: userID, Name: name, Locale: locale})
		u = User(row)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("update user %s: %w", userID, err)
	}
	return u, nil
}

// User returns an enabled account, or ErrNotFound.
func (s *Service) User(ctx context.Context, userID uuid.UUID) (User, error) {
	var u User
	err := s.db.InGlobalTx(ctx, func(tx pgx.Tx) error {
		row, err := store.New(tx).GetUser(ctx, userID)
		u = User(row)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("get user %s: %w", userID, err)
	}
	return u, nil
}

// MarkEmailVerified records that the user owns their address. Repeating it
// keeps the first time. A disabled or unknown account is ErrNotFound.
func (s *Service) MarkEmailVerified(ctx context.Context, userID uuid.UUID) error {
	var n int64
	err := s.db.InGlobalTx(ctx, func(tx pgx.Tx) error {
		var err error
		n, err = store.New(tx).MarkEmailVerified(ctx, userID)
		return err
	})
	if err != nil {
		return fmt.Errorf("mark email of %s verified: %w", userID, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchLastLogin records a successful login.
func (s *Service) TouchLastLogin(ctx context.Context, userID uuid.UUID) error {
	err := s.db.InGlobalTx(ctx, func(tx pgx.Tx) error {
		return store.New(tx).TouchLastLogin(ctx, userID)
	})
	if err != nil {
		return fmt.Errorf("touch last login of %s: %w", userID, err)
	}
	return nil
}

// Membership returns the user's active membership in a live workspace, or
// ErrNotFound: not a member, deactivated, account disabled or workspace
// deleted. It is the access check of every authenticated request.
func (s *Service) Membership(ctx context.Context, workspaceID, userID uuid.UUID) (Membership, error) {
	var m Membership
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		r, err := store.New(tx).GetMembership(ctx, store.GetMembershipParams{WorkspaceID: workspaceID, UserID: userID})
		if err != nil {
			return err
		}
		m = Membership{
			User: User{
				ID: r.UserID, Email: r.Email, Name: r.UserName, Locale: r.UserLocale,
				EmailVerifiedAt: r.EmailVerifiedAt,
			},
			Workspace: Workspace{
				ID: r.WorkspaceID, Slug: r.Slug, Name: r.WorkspaceName,
				Locale: r.WorkspaceLocale, Timezone: r.Timezone, CreatedAt: r.WorkspaceCreatedAt,
			},
			Role: r.RoleKey,
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, db.ErrNoWorkspace) {
		return Membership{}, ErrNotFound
	}
	if err != nil {
		return Membership{}, fmt.Errorf("get membership in %s: %w", workspaceID, err)
	}
	return m, nil
}

// UserWorkspaces lists the workspaces the user is an active member of,
// oldest membership first.
func (s *Service) UserWorkspaces(ctx context.Context, userID uuid.UUID) ([]WorkspaceSummary, error) {
	var out []WorkspaceSummary
	err := s.db.InUserTx(ctx, userID, func(tx pgx.Tx) error {
		rows, err := store.New(tx).ListUserWorkspaces(ctx, userID)
		if err != nil {
			return err
		}
		out = make([]WorkspaceSummary, 0, len(rows))
		for _, r := range rows {
			out = append(out, WorkspaceSummary{ID: r.ID, Slug: r.Slug, Name: r.Name, Role: r.RoleKey})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list workspaces of %s: %w", userID, err)
	}
	return out, nil
}

// UpdateWorkspace changes the settings of a workspace.
func (s *Service) UpdateWorkspace(ctx context.Context, workspaceID uuid.UUID, in UpdateWorkspaceInput) (Workspace, error) {
	var ws Workspace
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		cur, err := q.GetWorkspace(ctx, workspaceID)
		if err != nil {
			return err
		}
		next := CreateWorkspaceInput{
			Name: cur.Name, Slug: cur.Slug, Locale: cur.Locale, Timezone: cur.Timezone,
			OwnerID: uuid.Max, // normalize requires an owner; it is not used here
		}
		if in.Name != nil {
			next.Name = strings.TrimSpace(*in.Name)
		}
		if in.Locale != nil {
			next.Locale = *in.Locale
		}
		if in.Timezone != nil {
			next.Timezone = *in.Timezone
		}
		if err := next.normalize(); err != nil {
			return err
		}
		row, err := q.UpdateWorkspace(ctx, store.UpdateWorkspaceParams{
			WorkspaceID: workspaceID, Name: next.Name, Locale: next.Locale, Timezone: next.Timezone,
		})
		ws = Workspace(row)
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Workspace{}, ErrNotFound
	case errors.Is(err, ErrInvalid):
		return Workspace{}, err
	case err != nil:
		return Workspace{}, fmt.Errorf("update workspace %s: %w", workspaceID, err)
	}
	return ws, nil
}
