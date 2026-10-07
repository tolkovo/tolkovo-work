package tenancy

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy/store"
)

// Invitations into a workspace (docs/06 A-03). Tenancy stores them; the
// auth module issues the link tokens, sends the emails and signs the
// invited person in.

// ErrInvitationNotFound is returned for an unknown, revoked, accepted or
// expired invitation, and for a token that does not match one.
var ErrInvitationNotFound = errors.New("tenancy: invitation not found")

// InvitableRoles are the roles an invitation may grant. Ownership is never
// handed out by email.
var InvitableRoles = []string{RoleAdmin, RoleOperator}

// Invitation is an open invitation as the team page lists it.
type Invitation struct {
	ID            uuid.UUID
	Email         string
	Role          string
	InvitedByName string
	ExpiresAt     time.Time
	CreatedAt     time.Time
}

// Expired reports whether the link can no longer be accepted.
func (i Invitation) Expired(now time.Time) bool { return !now.Before(i.ExpiresAt) }

// IssuedInvitation is an invitation with a freshly issued link token, plus
// what the email needs.
type IssuedInvitation struct {
	Invitation
	WorkspaceName   string
	WorkspaceLocale string
}

// PendingInvitation is what the invited person sees before accepting.
type PendingInvitation struct {
	ID            uuid.UUID
	WorkspaceID   uuid.UUID
	WorkspaceName string
	Email         string
	Role          string
	ExpiresAt     time.Time
}

// CreateInvitationInput describes a new invitation.
type CreateInvitationInput struct {
	WorkspaceID uuid.UUID
	InvitedBy   uuid.UUID
	Email       string
	Role        string
	ExpiresAt   time.Time
	// AfterCreate runs in the same transaction, after the invitation is
	// stored. The auth module queues the email job here, so an invitation
	// is never saved without its email (ADR-0004).
	AfterCreate func(ctx context.Context, tx pgx.Tx, inv Invitation) error
}

// CreateInvitation stores an invitation. An open invitation to the same
// address is revoked first, so inviting again replaces it. An address that
// already belongs to an active member is ErrAlreadyMember.
func (s *Service) CreateInvitation(ctx context.Context, in CreateInvitationInput) (Invitation, error) {
	email, err := NormalizeEmail(in.Email)
	if err != nil {
		return Invitation{}, err
	}
	if !slices.Contains(InvitableRoles, in.Role) {
		return Invitation{}, fmt.Errorf("%w: role must be one of %s", ErrInvalid, strings.Join(InvitableRoles, ", "))
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Invitation{}, fmt.Errorf("new invitation id: %w", err)
	}
	var inv Invitation
	err = s.db.InTenantTx(ctx, in.WorkspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		member, err := q.ActiveMemberByEmail(ctx, store.ActiveMemberByEmailParams{WorkspaceID: in.WorkspaceID, Email: email})
		if err != nil {
			return err
		}
		if member {
			return ErrAlreadyMember
		}
		if err := q.RevokeOpenInvitationByEmail(ctx, store.RevokeOpenInvitationByEmailParams{
			WorkspaceID: in.WorkspaceID, Email: email,
		}); err != nil {
			return err
		}
		row, err := q.CreateInvitation(ctx, store.CreateInvitationParams{
			ID: id, WorkspaceID: in.WorkspaceID, Email: email, RoleKey: in.Role,
			InvitedBy: pgtype.UUID{Bytes: in.InvitedBy, Valid: in.InvitedBy != uuid.Nil},
			ExpiresAt: in.ExpiresAt,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %q", ErrUnknownRole, in.Role)
		}
		if err != nil {
			return err
		}
		inv = Invitation{ID: row.ID, Email: row.Email, Role: in.Role, ExpiresAt: row.ExpiresAt, CreatedAt: row.CreatedAt}
		if in.AfterCreate != nil {
			return in.AfterCreate(ctx, tx, inv)
		}
		return nil
	})
	switch {
	case errors.Is(err, ErrAlreadyMember), errors.Is(err, ErrUnknownRole):
		return Invitation{}, err
	case err != nil:
		return Invitation{}, fmt.Errorf("create invitation in %s: %w", in.WorkspaceID, err)
	}
	return inv, nil
}

// Invitations lists the open invitations of a workspace, newest first,
// expired ones included (the team page offers to resend them).
func (s *Service) Invitations(ctx context.Context, workspaceID uuid.UUID) ([]Invitation, error) {
	var out []Invitation
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		rows, err := store.New(tx).ListOpenInvitations(ctx, workspaceID)
		if err != nil {
			return err
		}
		out = make([]Invitation, 0, len(rows))
		for _, r := range rows {
			out = append(out, Invitation{
				ID: r.ID, Email: r.Email, Role: r.RoleKey, InvitedByName: r.InvitedByName.String,
				ExpiresAt: r.ExpiresAt, CreatedAt: r.CreatedAt,
			})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list invitations of %s: %w", workspaceID, err)
	}
	return out, nil
}

// RevokeInvitation withdraws an open invitation; its link stops working.
func (s *Service) RevokeInvitation(ctx context.Context, workspaceID, id uuid.UUID) error {
	var n int64
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		n, err = store.New(tx).RevokeInvitation(ctx, store.RevokeInvitationParams{WorkspaceID: workspaceID, ID: id})
		return err
	})
	if err != nil {
		return fmt.Errorf("revoke invitation %s: %w", id, err)
	}
	if n == 0 {
		return ErrInvitationNotFound
	}
	return nil
}

// IssueInvitationToken stores the hash of a new link token for an open
// invitation (expired ones too) and extends it until expiresAt. The previous
// link stops working. afterIssue, when set, runs in the same transaction.
func (s *Service) IssueInvitationToken(
	ctx context.Context, workspaceID, id uuid.UUID, tokenHash []byte, expiresAt time.Time,
	afterIssue func(ctx context.Context, tx pgx.Tx) error,
) (IssuedInvitation, error) {
	var out IssuedInvitation
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		r, err := store.New(tx).IssueInvitationToken(ctx, store.IssueInvitationTokenParams{
			WorkspaceID: workspaceID, ID: id, TokenHash: tokenHash, ExpiresAt: expiresAt,
		})
		if err != nil {
			return err
		}
		out = IssuedInvitation{
			Invitation: Invitation{
				ID: r.ID, Email: r.Email, Role: r.RoleKey, InvitedByName: r.InvitedByName,
				ExpiresAt: r.ExpiresAt, CreatedAt: r.CreatedAt,
			},
			WorkspaceName: r.WorkspaceName, WorkspaceLocale: r.WorkspaceLocale,
		}
		if afterIssue != nil {
			return afterIssue(ctx, tx)
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, db.ErrNoWorkspace) {
		return IssuedInvitation{}, ErrInvitationNotFound
	}
	if err != nil {
		return IssuedInvitation{}, fmt.Errorf("issue invitation token %s: %w", id, err)
	}
	return out, nil
}

// PendingInvitation finds the open, unexpired invitation of a workspace by
// the hash of its link token.
func (s *Service) PendingInvitation(ctx context.Context, workspaceID uuid.UUID, tokenHash []byte) (PendingInvitation, error) {
	var out PendingInvitation
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		out, err = pendingInvitation(ctx, store.New(tx), workspaceID, tokenHash)
		return err
	})
	if err != nil {
		return PendingInvitation{}, err
	}
	return out, nil
}

func pendingInvitation(ctx context.Context, q *store.Queries, workspaceID uuid.UUID, tokenHash []byte) (PendingInvitation, error) {
	r, err := q.GetInvitationByToken(ctx, store.GetInvitationByTokenParams{WorkspaceID: workspaceID, TokenHash: tokenHash})
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, db.ErrNoWorkspace) {
		return PendingInvitation{}, ErrInvitationNotFound
	}
	if err != nil {
		return PendingInvitation{}, fmt.Errorf("get invitation in %s: %w", workspaceID, err)
	}
	return PendingInvitation{
		ID: r.ID, WorkspaceID: workspaceID, WorkspaceName: r.WorkspaceName,
		Email: r.Email, Role: r.RoleKey, ExpiresAt: r.ExpiresAt,
	}, nil
}

// NewAccount is the account created when an invited person has none yet.
type NewAccount struct {
	Name         string
	Locale       string // "ru" or "en"; defaults to the workspace locale
	PasswordHash string // produced by the auth module, never a plain password
}

// AcceptInvitationInput accepts an invitation either for an existing
// account (UserID, whose password the auth module has checked) or by
// creating one (NewAccount). Exactly one of them is set.
type AcceptInvitationInput struct {
	WorkspaceID uuid.UUID
	TokenHash   []byte
	UserID      uuid.UUID
	NewAccount  *NewAccount
}

// AcceptInvitation re-checks the invitation under a row lock, creates the
// account if needed, adds or reactivates the membership and marks the
// invitation accepted, all in one transaction. A new account's email counts
// as confirmed: the link arrived at that address. The invitation is for one
// address: an existing account with another email is ErrInvitationNotFound.
func (s *Service) AcceptInvitation(ctx context.Context, in AcceptInvitationInput) (Membership, error) {
	if (in.UserID == uuid.Nil) == (in.NewAccount == nil) {
		return Membership{}, errors.New("tenancy: accept needs either a user or a new account")
	}
	var na NewAccount
	if in.NewAccount != nil {
		na = *in.NewAccount
		na.Name = strings.TrimSpace(na.Name)
		if na.Name == "" || utf8.RuneCountInString(na.Name) > 120 {
			return Membership{}, fmt.Errorf("%w: name must be 1-120 characters", ErrInvalid)
		}
		if na.Locale != "" && na.Locale != "ru" && na.Locale != "en" {
			return Membership{}, fmt.Errorf("%w: locale must be ru or en", ErrInvalid)
		}
		if na.PasswordHash == "" {
			return Membership{}, fmt.Errorf("%w: password hash is required", ErrInvalid)
		}
	}
	var userID uuid.UUID
	err := s.db.InTenantTx(ctx, in.WorkspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		inv, err := pendingInvitation(ctx, q, in.WorkspaceID, in.TokenHash)
		if err != nil {
			return err
		}
		userID = in.UserID
		if in.NewAccount != nil {
			if userID, err = uuid.NewV7(); err != nil {
				return fmt.Errorf("new user id: %w", err)
			}
			ws, err := q.GetWorkspace(ctx, in.WorkspaceID)
			if err != nil {
				return err
			}
			locale := na.Locale
			if locale == "" {
				locale = ws.Locale
			}
			now := time.Now()
			if _, err := q.CreateUser(ctx, store.CreateUserParams{
				ID: userID, Email: inv.Email, Name: na.Name, Locale: locale,
				PasswordHash:    pgtype.Text{String: na.PasswordHash, Valid: true},
				EmailVerifiedAt: &now,
			}); err != nil {
				return err
			}
		} else {
			u, err := q.GetUser(ctx, userID)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrInvitationNotFound
			}
			if err != nil {
				return err
			}
			if !strings.EqualFold(u.Email, inv.Email) {
				return ErrInvitationNotFound
			}
		}
		n, err := q.UpsertMember(ctx, store.UpsertMemberParams{WorkspaceID: in.WorkspaceID, UserID: userID, RoleKey: inv.Role})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrAlreadyMember
		}
		// A member who comes back starts without the tokens they had before.
		if err := q.DeleteMemberAccessTokens(ctx, store.DeleteMemberAccessTokensParams{WorkspaceID: in.WorkspaceID, UserID: userID}); err != nil {
			return err
		}
		return q.MarkInvitationAccepted(ctx, store.MarkInvitationAcceptedParams{
			WorkspaceID: in.WorkspaceID, ID: inv.ID, UserID: pgtype.UUID{Bytes: userID, Valid: true},
		})
	})
	switch {
	case isUnique(err, "users_email_key"):
		return Membership{}, ErrEmailTaken // an account appeared meanwhile: accept with its password
	case errors.Is(err, ErrInvitationNotFound), errors.Is(err, ErrAlreadyMember):
		return Membership{}, err
	case err != nil:
		return Membership{}, fmt.Errorf("accept invitation in %s: %w", in.WorkspaceID, err)
	}
	return s.Membership(ctx, in.WorkspaceID, userID)
}
