package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// Invitations into a workspace (docs/06 A-03). Tenancy stores them; auth
// issues the links and signs the invited person in.

// SentInvitation is an invitation after create or resend. URL is set only
// when the server cannot email: the admin passes the link on.
type SentInvitation struct {
	Invitation tenancy.Invitation
	URL        string
}

// MailEnabled reports whether the server emails links itself.
func (s *Service) MailEnabled() bool { return s.mailEnabled }

// Invite creates an invitation and emails its link in the same transaction
// (the job), or returns the link when email is off.
func (s *Service) Invite(ctx context.Context, workspaceID, invitedBy uuid.UUID, email, role string) (SentInvitation, error) {
	in := tenancy.CreateInvitationInput{
		WorkspaceID: workspaceID, InvitedBy: invitedBy, Email: email, Role: role,
		ExpiresAt: s.now().Add(InvitationTTL),
	}
	if s.mailEnabled {
		in.AfterCreate = func(ctx context.Context, tx pgx.Tx, inv tenancy.Invitation) error {
			_, err := s.jobs.InsertTx(ctx, tx, InvitationEmailArgs{WorkspaceID: workspaceID, InvitationID: inv.ID}, nil)
			return err
		}
	}
	inv, err := s.tenancy.CreateInvitation(ctx, in)
	if err != nil {
		return SentInvitation{}, err
	}
	if s.mailEnabled {
		return SentInvitation{Invitation: inv}, nil
	}
	return s.issueInvitationLink(ctx, workspaceID, inv.ID)
}

// ResendInvitation issues a new link for an open invitation: the previous
// one stops working at once and the invitation runs for another full
// period. The link is emailed, or returned when email is off.
func (s *Service) ResendInvitation(ctx context.Context, workspaceID, id uuid.UUID) (SentInvitation, error) {
	if !s.mailEnabled {
		return s.issueInvitationLink(ctx, workspaceID, id)
	}
	// A token nobody knows retires the old link now; the job issues the one
	// it emails.
	_, hash, err := newLinkToken(workspaceID)
	if err != nil {
		return SentInvitation{}, err
	}
	inv, err := s.tenancy.IssueInvitationToken(ctx, workspaceID, id, hash, s.now().Add(InvitationTTL),
		func(ctx context.Context, tx pgx.Tx) error {
			_, err := s.jobs.InsertTx(ctx, tx, InvitationEmailArgs{WorkspaceID: workspaceID, InvitationID: id}, nil)
			return err
		})
	if err != nil {
		return SentInvitation{}, err
	}
	return SentInvitation{Invitation: inv.Invitation}, nil
}

func (s *Service) issueInvitationLink(ctx context.Context, workspaceID, id uuid.UUID) (SentInvitation, error) {
	token, hash, err := newLinkToken(workspaceID)
	if err != nil {
		return SentInvitation{}, err
	}
	inv, err := s.tenancy.IssueInvitationToken(ctx, workspaceID, id, hash, s.now().Add(InvitationTTL), nil)
	if err != nil {
		return SentInvitation{}, err
	}
	return SentInvitation{Invitation: inv.Invitation, URL: s.links.link(invitePath, token)}, nil
}

// InvitationPreview is what the invited person sees before accepting.
type InvitationPreview struct {
	Invitation    tenancy.PendingInvitation
	AccountExists bool
}

// PreviewInvitation resolves an invitation link.
func (s *Service) PreviewInvitation(ctx context.Context, token string, c Client) (InvitationPreview, error) {
	if err := s.allow(ctx, "linktoken:ip:"+digest(c.IP), linkTokenPerIP); err != nil {
		return InvitationPreview{}, err
	}
	inv, err := s.pendingInvitation(ctx, token)
	if err != nil {
		return InvitationPreview{}, err
	}
	_, err = s.tenancy.Credentials(ctx, inv.Email)
	switch {
	case errors.Is(err, tenancy.ErrNotFound):
		return InvitationPreview{Invitation: inv}, nil
	case err != nil:
		return InvitationPreview{}, err
	}
	return InvitationPreview{Invitation: inv, AccountExists: true}, nil
}

// AcceptInvitationInput accepts an invitation link.
type AcceptInvitationInput struct {
	Token    string
	Password string
	Name     string // for a new account
	Locale   string // for a new account; the workspace's when empty
}

// AcceptInvitation joins the invitation's workspace and signs in there.
// With an account for the invited email the password must be that
// account's (checked like a login); without one a new account is created.
func (s *Service) AcceptInvitation(ctx context.Context, in AcceptInvitationInput, c Client) (SignIn, error) {
	if err := s.allow(ctx, "linktoken:ip:"+digest(c.IP), linkTokenPerIP); err != nil {
		return SignIn{}, err
	}
	workspaceID, hash, err := parseLinkToken(in.Token)
	if err != nil {
		return SignIn{}, err
	}
	inv, err := s.pendingInvitation(ctx, in.Token)
	if err != nil {
		return SignIn{}, err
	}
	emailKey := digest(strings.ToLower(inv.Email))
	if err := s.allow(ctx, "login:ie:"+digest(c.IP+"|"+emailKey), loginPerIPAndEmail); err != nil {
		return SignIn{}, err
	}
	if err := s.allow(ctx, "login:e:"+emailKey, loginPerEmail); err != nil {
		return SignIn{}, err
	}

	accept := tenancy.AcceptInvitationInput{WorkspaceID: workspaceID, TokenHash: hash}
	creds, err := s.tenancy.Credentials(ctx, inv.Email)
	switch {
	case errors.Is(err, tenancy.ErrNotFound):
		if err := validatePassword(in.Password); err != nil {
			return SignIn{}, err
		}
		pwHash, err := s.hasher.Hash(ctx, in.Password)
		if err != nil {
			return SignIn{}, err
		}
		accept.NewAccount = &tenancy.NewAccount{Name: in.Name, Locale: in.Locale, PasswordHash: pwHash}
	case err != nil:
		return SignIn{}, err
	default:
		ok, err := s.hasher.Verify(ctx, in.Password, creds.PasswordHash)
		if err != nil {
			return SignIn{}, fmt.Errorf("verify password of %s: %w", creds.UserID, err)
		}
		if !ok || creds.Disabled {
			return SignIn{}, ErrInvalidCredentials
		}
		accept.UserID = creds.UserID
	}

	m, err := s.tenancy.AcceptInvitation(ctx, accept)
	if errors.Is(err, tenancy.ErrInvitationNotFound) {
		return SignIn{}, ErrInvalidToken
	}
	if err != nil {
		return SignIn{}, err
	}
	if err := s.tenancy.TouchLastLogin(ctx, m.User.ID); err != nil {
		return SignIn{}, err
	}
	return s.start(ctx, m, c.UserAgent)
}

func (s *Service) pendingInvitation(ctx context.Context, token string) (tenancy.PendingInvitation, error) {
	workspaceID, hash, err := parseLinkToken(token)
	if err != nil {
		return tenancy.PendingInvitation{}, err
	}
	inv, err := s.tenancy.PendingInvitation(ctx, workspaceID, hash)
	if errors.Is(err, tenancy.ErrInvitationNotFound) {
		return tenancy.PendingInvitation{}, ErrInvalidToken
	}
	return inv, err
}

// now is the service clock.
func (s *Service) now() time.Time { return s.clock() }
