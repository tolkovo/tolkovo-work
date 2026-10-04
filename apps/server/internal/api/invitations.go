package api

import (
	"net/http"
	"time"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/auth"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// Invitations (docs/06 A-03) and the public pages behind emailed links:
// accepting an invitation, password reset and email confirmation (A-06).

// publicBodyBytes bounds the bodies of routes served without a session.
const publicBodyBytes = 4 << 10

// ListInvitations implements GET /invitations.
func (s *server) ListInvitations(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.MembersManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	list, err := s.tenancy.Invitations(r.Context(), p.WorkspaceID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	now := time.Now()
	items := make([]oapi.Invitation, 0, len(list))
	for _, inv := range list {
		items = append(items, invitationBody(inv, now))
	}
	writeJSON(w, http.StatusOK, oapi.InvitationList{Items: items, EmailEnabled: s.auth.MailEnabled()})
}

// CreateInvitation implements POST /invitations.
func (s *server) CreateInvitation(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.MembersManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var req oapi.CreateInvitationRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	sent, err := s.auth.Invite(r.Context(), p.WorkspaceID, p.UserID, req.Email, string(req.Role))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, sentInvitationBody(sent))
}

// ResendInvitation implements POST /invitations/{id}/resend.
func (s *server) ResendInvitation(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, err := authz.Require(r.Context(), authz.MembersManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	sent, err := s.auth.ResendInvitation(r.Context(), p.WorkspaceID, id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sentInvitationBody(sent))
}

// RevokeInvitation implements DELETE /invitations/{id}.
func (s *server) RevokeInvitation(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, err := authz.Require(r.Context(), authz.MembersManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.tenancy.RevokeInvitation(r.Context(), p.WorkspaceID, id); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PreviewInvitation implements POST /auth/invitation/preview.
func (s *server) PreviewInvitation(w http.ResponseWriter, r *http.Request) {
	var req oapi.TokenRequest
	if err := decodeJSONLimit(w, r, &req, publicBodyBytes); err != nil {
		s.writeError(w, r, err)
		return
	}
	p, err := s.auth.PreviewInvitation(r.Context(), req.Token, s.browser(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, oapi.InvitationPreview{
		WorkspaceName: p.Invitation.WorkspaceName, Email: p.Invitation.Email, Role: oapi.Role(p.Invitation.Role),
		ExpiresAt: p.Invitation.ExpiresAt, AccountExists: p.AccountExists,
	})
}

// AcceptInvitation implements POST /auth/invitation/accept.
func (s *server) AcceptInvitation(w http.ResponseWriter, r *http.Request) {
	var req oapi.AcceptInvitationRequest
	if err := decodeJSONLimit(w, r, &req, publicBodyBytes); err != nil {
		s.writeError(w, r, err)
		return
	}
	in, err := s.auth.AcceptInvitation(r.Context(), auth.AcceptInvitationInput{
		Token: req.Token, Password: req.Password, Name: deref(req.Name), Locale: string(deref(req.Locale)),
	}, s.browser(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.revokeCookieSession(r)
	s.cookies.set(w, in.Token)
	writeJSON(w, http.StatusOK, sessionBody(in.Membership, in.Session))
}

// ForgotPassword implements POST /auth/password/forgot.
func (s *server) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req oapi.ForgotPasswordRequest
	if err := decodeJSONLimit(w, r, &req, publicBodyBytes); err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.auth.ForgotPassword(r.Context(), req.Email, s.browser(r)); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// ResetPassword implements POST /auth/password/reset.
func (s *server) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req oapi.ResetPasswordRequest
	if err := decodeJSONLimit(w, r, &req, publicBodyBytes); err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.auth.ResetPassword(r.Context(), req.Token, req.Password, s.browser(r)); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// VerifyEmail implements POST /auth/email/verify.
func (s *server) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	var req oapi.TokenRequest
	if err := decodeJSONLimit(w, r, &req, publicBodyBytes); err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.auth.VerifyEmail(r.Context(), req.Token, s.browser(r)); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ResendEmailVerification implements POST /me/email/verification.
func (s *server) ResendEmailVerification(w http.ResponseWriter, r *http.Request) {
	st, ok := authStateFrom(r.Context())
	if !ok {
		s.writeError(w, r, authz.ErrUnauthenticated)
		return
	}
	if err := s.auth.ResendEmailVerification(r.Context(), st.session.UserID); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func invitationBody(inv tenancy.Invitation, now time.Time) oapi.Invitation {
	return oapi.Invitation{
		Id: inv.ID, Email: inv.Email, Role: oapi.Role(inv.Role), InvitedByName: inv.InvitedByName,
		ExpiresAt: inv.ExpiresAt, CreatedAt: inv.CreatedAt, Expired: inv.Expired(now),
	}
}

func sentInvitationBody(sent auth.SentInvitation) oapi.InvitationSent {
	out := oapi.InvitationSent{Invitation: invitationBody(sent.Invitation, time.Now())}
	if sent.URL != "" {
		out.InviteUrl = &sent.URL
	}
	return out
}
