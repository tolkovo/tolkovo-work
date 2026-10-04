package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
)

// tokenOf returns the fragment of an invitation link.
func tokenOf(t *testing.T, link string) string {
	t.Helper()
	prefix := panelURL + "/invite#"
	if !strings.HasPrefix(link, prefix) || len(link) == len(prefix) {
		t.Fatalf("unexpected invitation link %q", link)
	}
	return link[len(prefix):]
}

func TestInvitation_EmailedLinkCreatesAccountAndSignsIn(t *testing.T) {
	e := newEnv(t, envOptions{mail: true})
	owner, sess := e.register("owner@example.com", "acme")

	rec := owner.do(http.MethodPost, "/api/v1/invitations", map[string]any{"email": "Op@Example.com", "role": "operator"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite: %d %s", rec.Code, rec.Body)
	}
	sent := decode[oapi.InvitationSent](t, rec)
	if sent.InviteUrl != nil {
		t.Fatalf("with email on, the link must not be returned: %v", *sent.InviteUrl)
	}
	msg, token := e.waitMail("Op@Example.com", "/invite")
	if !strings.Contains(msg.Subject, "WS acme") || !strings.Contains(msg.Text, "User acme") {
		t.Fatalf("email must name the workspace and the inviter: %q\n%s", msg.Subject, msg.Text)
	}

	list := decode[oapi.InvitationList](t, owner.do(http.MethodGet, "/api/v1/invitations", nil))
	if !list.EmailEnabled || len(list.Items) != 1 || list.Items[0].Role != oapi.RoleOperator || list.Items[0].Expired {
		t.Fatalf("list: %+v", list)
	}

	guest := e.client()
	rec = guest.do(http.MethodPost, "/api/v1/auth/invitation/preview", map[string]any{"token": token})
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body)
	}
	if p := decode[oapi.InvitationPreview](t, rec); p.AccountExists || p.WorkspaceName != "WS acme" || p.Email != "Op@Example.com" {
		t.Fatalf("preview: %+v", p)
	}
	// A new account needs a name.
	rec = guest.do(http.MethodPost, "/api/v1/auth/invitation/accept", map[string]any{"token": token, "password": "operator pass"})
	wantProblem(t, rec, http.StatusBadRequest, "invalid_request")

	rec = guest.do(http.MethodPost, "/api/v1/auth/invitation/accept", map[string]any{
		"token": token, "password": "operator pass", "name": "Olga",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body)
	}
	got := decode[oapi.Session](t, rec)
	if got.Workspace.Id != sess.Workspace.Id || got.Role != oapi.RoleOperator || !got.User.EmailVerified || got.User.Name != "Olga" {
		t.Fatalf("session after accept: %+v", got)
	}
	if rec := guest.do(http.MethodGet, "/api/v1/me", nil); rec.Code != http.StatusOK {
		t.Fatalf("signed in after accept: %d", rec.Code)
	}

	// The link works once and the invitation leaves the list.
	rec = e.client().do(http.MethodPost, "/api/v1/auth/invitation/accept", map[string]any{
		"token": token, "password": "operator pass", "name": "Olga",
	})
	wantProblem(t, rec, http.StatusUnprocessableEntity, "invalid_token")
	if list := decode[oapi.InvitationList](t, owner.do(http.MethodGet, "/api/v1/invitations", nil)); len(list.Items) != 0 {
		t.Fatalf("accepted invitation still listed: %+v", list.Items)
	}
	// Inviting an active member is refused.
	rec = owner.do(http.MethodPost, "/api/v1/invitations", map[string]any{"email": "op@example.com", "role": "admin"})
	wantProblem(t, rec, http.StatusConflict, "already_member")
}

func TestInvitation_WithoutEmailReturnsLinkResendAndRevoke(t *testing.T) {
	e := newEnv(t, envOptions{})
	owner, _ := e.register("owner@example.com", "acme")

	rec := owner.do(http.MethodPost, "/api/v1/invitations", map[string]any{"email": "a@example.com", "role": "admin"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite: %d %s", rec.Code, rec.Body)
	}
	sent := decode[oapi.InvitationSent](t, rec)
	if sent.InviteUrl == nil {
		t.Fatal("with email off, the link must be returned")
	}
	first := tokenOf(t, *sent.InviteUrl)
	if list := decode[oapi.InvitationList](t, owner.do(http.MethodGet, "/api/v1/invitations", nil)); list.EmailEnabled {
		t.Fatal("email_enabled must be false")
	}

	rec = owner.do(http.MethodPost, "/api/v1/invitations/"+sent.Invitation.Id.String()+"/resend", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("resend: %d %s", rec.Code, rec.Body)
	}
	resent := decode[oapi.InvitationSent](t, rec)
	second := tokenOf(t, *resent.InviteUrl)
	guest := e.client()
	wantProblem(t, guest.do(http.MethodPost, "/api/v1/auth/invitation/preview", map[string]any{"token": first}),
		http.StatusUnprocessableEntity, "invalid_token")
	if rec := guest.do(http.MethodPost, "/api/v1/auth/invitation/preview", map[string]any{"token": second}); rec.Code != http.StatusOK {
		t.Fatalf("new link: %d %s", rec.Code, rec.Body)
	}

	// Inviting the same address again replaces the open invitation.
	rec = owner.do(http.MethodPost, "/api/v1/invitations", map[string]any{"email": "A@example.com", "role": "operator"})
	third := tokenOf(t, *decode[oapi.InvitationSent](t, rec).InviteUrl)
	wantProblem(t, guest.do(http.MethodPost, "/api/v1/auth/invitation/preview", map[string]any{"token": second}),
		http.StatusUnprocessableEntity, "invalid_token")
	list := decode[oapi.InvitationList](t, owner.do(http.MethodGet, "/api/v1/invitations", nil))
	if len(list.Items) != 1 || list.Items[0].Role != oapi.RoleOperator {
		t.Fatalf("one open invitation expected: %+v", list.Items)
	}

	rec = owner.do(http.MethodDelete, "/api/v1/invitations/"+list.Items[0].Id.String(), nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, guest.do(http.MethodPost, "/api/v1/auth/invitation/preview", map[string]any{"token": third}),
		http.StatusUnprocessableEntity, "invalid_token")
	wantProblem(t, owner.do(http.MethodDelete, "/api/v1/invitations/"+list.Items[0].Id.String(), nil),
		http.StatusNotFound, "not_found")
}

func TestInvitation_RulesAndPermissions(t *testing.T) {
	e := newEnv(t, envOptions{})
	owner, sess := e.register("owner@example.com", "acme")
	_, opSess := e.register("op@example.com", "opws")
	e.addMember(sess.Workspace.Id, opSess.User.Id, "operator")

	// Ownership is never handed out by email; the address must be valid.
	wantProblem(t, owner.do(http.MethodPost, "/api/v1/invitations", map[string]any{"email": "x@example.com", "role": "owner"}),
		http.StatusBadRequest, "invalid_request")
	wantProblem(t, owner.do(http.MethodPost, "/api/v1/invitations", map[string]any{"email": "not an email", "role": "operator"}),
		http.StatusBadRequest, "invalid_request")

	op, _ := e.login("op@example.com")
	if rec := op.do(http.MethodPost, "/api/v1/auth/workspace", map[string]any{"workspace_id": sess.Workspace.Id}); rec.Code != http.StatusOK {
		t.Fatalf("switch: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, op.do(http.MethodGet, "/api/v1/invitations", nil), http.StatusForbidden, "forbidden")
	wantProblem(t, op.do(http.MethodPost, "/api/v1/invitations", map[string]any{"email": "x@example.com", "role": "operator"}),
		http.StatusForbidden, "forbidden")

	// Another workspace's invitation is not found.
	other, _ := e.register("other@example.com", "other")
	rec := other.do(http.MethodPost, "/api/v1/invitations", map[string]any{"email": "z@example.com", "role": "operator"})
	foreign := decode[oapi.InvitationSent](t, rec).Invitation.Id
	wantProblem(t, owner.do(http.MethodDelete, "/api/v1/invitations/"+foreign.String(), nil), http.StatusNotFound, "not_found")
	wantProblem(t, owner.do(http.MethodPost, "/api/v1/invitations/"+foreign.String()+"/resend", nil), http.StatusNotFound, "not_found")

	// Garbage tokens are invalid, not errors.
	for _, tok := range []string{"x", strings.Repeat("A", 64), "%%%"} {
		wantProblem(t, e.client().do(http.MethodPost, "/api/v1/auth/invitation/preview", map[string]any{"token": tok}),
			http.StatusUnprocessableEntity, "invalid_token")
	}
}

func TestInvitation_ExistingAccountJoinsWithItsPassword(t *testing.T) {
	e := newEnv(t, envOptions{})
	owner, sess := e.register("owner@example.com", "acme")
	e.register("anna@example.com", "annaws")

	rec := owner.do(http.MethodPost, "/api/v1/invitations", map[string]any{"email": "anna@example.com", "role": "admin"})
	token := tokenOf(t, *decode[oapi.InvitationSent](t, rec).InviteUrl)

	guest := e.client()
	if p := decode[oapi.InvitationPreview](t, guest.do(http.MethodPost, "/api/v1/auth/invitation/preview",
		map[string]any{"token": token})); !p.AccountExists {
		t.Fatalf("preview must report the existing account: %+v", p)
	}
	rec = guest.do(http.MethodPost, "/api/v1/auth/invitation/accept", map[string]any{"token": token, "password": "wrong password"})
	wantProblem(t, rec, http.StatusUnauthorized, "invalid_credentials")

	rec = guest.do(http.MethodPost, "/api/v1/auth/invitation/accept", map[string]any{
		"token": token, "password": "correct horse", "name": "Ignored",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body)
	}
	got := decode[oapi.Session](t, rec)
	if got.Workspace.Id != sess.Workspace.Id || got.Role != oapi.RoleAdmin || got.User.Name != "User annaws" {
		t.Fatalf("session: %+v", got)
	}
	ws := decode[oapi.WorkspaceList](t, guest.do(http.MethodGet, "/api/v1/me/workspaces", nil))
	if len(ws.Items) != 2 {
		t.Fatalf("anna must now be in two workspaces: %+v", ws.Items)
	}
}

func TestPasswordReset_EmailedLinkSetsPasswordAndRevokesSessions(t *testing.T) {
	e := newEnv(t, envOptions{mail: true})
	old, _ := e.register("anna@example.com", "acme")
	e.waitMail("anna@example.com", "/verify-email") // the confirmation email of registration

	guest := e.client()
	if rec := guest.do(http.MethodPost, "/api/v1/auth/password/forgot", map[string]any{"email": "nobody@example.com"}); rec.Code != http.StatusAccepted {
		t.Fatalf("unknown email: %d %s", rec.Code, rec.Body)
	}
	if rec := guest.do(http.MethodPost, "/api/v1/auth/password/forgot", map[string]any{"email": "ANNA@example.com"}); rec.Code != http.StatusAccepted {
		t.Fatalf("forgot: %d %s", rec.Code, rec.Body)
	}
	_, token := e.waitMail("anna@example.com", "/reset-password")

	wantProblem(t, guest.do(http.MethodPost, "/api/v1/auth/password/reset", map[string]any{"token": token, "password": "short"}),
		http.StatusBadRequest, "invalid_request")
	if rec := guest.do(http.MethodPost, "/api/v1/auth/password/reset", map[string]any{"token": token, "password": "brand new pass"}); rec.Code != http.StatusNoContent {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, guest.do(http.MethodPost, "/api/v1/auth/password/reset", map[string]any{"token": token, "password": "another pass"}),
		http.StatusUnprocessableEntity, "invalid_token")
	if rec := old.do(http.MethodGet, "/api/v1/me", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("old session must be revoked: %d", rec.Code)
	}
	rec := guest.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "anna@example.com", "password": "brand new pass"})
	if rec.Code != http.StatusOK {
		t.Fatalf("login with the new password: %d %s", rec.Code, rec.Body)
	}
	if s := decode[oapi.Session](t, rec); !s.User.EmailVerified {
		t.Fatal("a used reset link confirms the email")
	}
}

func TestPasswordReset_WithoutEmailIsUnavailable(t *testing.T) {
	e := newEnv(t, envOptions{})
	e.register("anna@example.com", "acme")
	wantProblem(t, e.client().do(http.MethodPost, "/api/v1/auth/password/forgot", map[string]any{"email": "anna@example.com"}),
		http.StatusServiceUnavailable, "email_disabled")
}

func TestEmailVerification(t *testing.T) {
	e := newEnv(t, envOptions{mail: true})
	c, sess := e.register("anna@example.com", "acme")
	if sess.User.EmailVerified {
		t.Fatal("a new registration is not confirmed")
	}
	_, first := e.waitMail("anna@example.com", "/verify-email")

	// A resend replaces the link.
	if rec := c.do(http.MethodPost, "/api/v1/me/email/verification", nil); rec.Code != http.StatusAccepted {
		t.Fatalf("resend: %d %s", rec.Code, rec.Body)
	}
	_, second := e.waitMail("anna@example.com", "/verify-email")
	guest := e.client() // the link may open in another browser
	wantProblem(t, guest.do(http.MethodPost, "/api/v1/auth/email/verify", map[string]any{"token": first}),
		http.StatusUnprocessableEntity, "invalid_token")
	if rec := guest.do(http.MethodPost, "/api/v1/auth/email/verify", map[string]any{"token": second}); rec.Code != http.StatusNoContent {
		t.Fatalf("verify: %d %s", rec.Code, rec.Body)
	}
	if me := decode[oapi.Session](t, c.do(http.MethodGet, "/api/v1/me", nil)); !me.User.EmailVerified {
		t.Fatal("email must be confirmed")
	}
	wantProblem(t, c.do(http.MethodPost, "/api/v1/me/email/verification", nil), http.StatusConflict, "email_verified")
}
