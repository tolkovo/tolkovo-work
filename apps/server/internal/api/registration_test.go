package api

import (
	"net/http"
	"sync"
	"testing"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/auth"
)

func registrationOpen(t *testing.T, e *env) bool {
	t.Helper()
	rec := e.client().do(http.MethodGet, "/api/v1/auth/registration", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("registration state: %d %s", rec.Code, rec.Body)
	}
	return decode[oapi.RegistrationState](t, rec).Open
}

func registerBody(email, slug string) map[string]any {
	return map[string]any{"email": email, "password": "correct horse", "name": "User " + slug, "workspace_name": "WS " + slug, "workspace_slug": slug}
}

// Self-host registration (TOLKOVO_REGISTRATION=first): the first account
// becomes the owner, even when two people try at once; after that the
// panel hides "Create workspace", the API refuses and people join by
// invitation.
func TestRegistration_FirstAccountOnly(t *testing.T) {
	e := newEnv(t, envOptions{registration: auth.RegistrationFirst})
	if !registrationOpen(t, e) {
		t.Fatal("an empty server must take the first account")
	}

	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i, slug := range []string{"first-a", "first-b"} {
		wg.Go(func() {
			codes[i] = e.client().do(http.MethodPost, "/api/v1/auth/register", registerBody(slug+"@example.test", slug)).Code
		})
	}
	wg.Wait()
	if (codes[0] == http.StatusCreated) == (codes[1] == http.StatusCreated) {
		t.Fatalf("exactly one first account: %v", codes)
	}
	if n := e.ownerCount("SELECT count(*) FROM users"); n != 1 {
		t.Fatalf("users: %d", n)
	}
	if n := e.ownerCount("SELECT count(*) FROM workspaces"); n != 1 {
		t.Fatalf("workspaces: %d", n)
	}
	if registrationOpen(t, e) {
		t.Fatal("registration still open after the first account")
	}
	wantProblem(t, e.client().do(http.MethodPost, "/api/v1/auth/register", registerBody("late@example.test", "late")),
		http.StatusForbidden, "registration_closed")

	// The owner invites a colleague, who gets an account all the same.
	owner, _ := e.login(map[bool]string{true: "first-a@example.test", false: "first-b@example.test"}[codes[0] == http.StatusCreated])
	rec := owner.do(http.MethodPost, "/api/v1/invitations", map[string]any{"email": "op@example.test", "role": "operator"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite: %d %s", rec.Code, rec.Body)
	}
	token := tokenOf(t, *decode[oapi.InvitationSent](t, rec).InviteUrl)
	rec = e.client().do(http.MethodPost, "/api/v1/auth/invitation/accept", map[string]any{"token": token, "password": "operator pass", "name": "Olga"})
	if rec.Code != http.StatusOK {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body)
	}
}

// TOLKOVO_REGISTRATION=closed refuses even the first account; open (the
// SaaS default) takes everyone.
func TestRegistration_ClosedAndOpen(t *testing.T) {
	closed := newEnv(t, envOptions{registration: auth.RegistrationClosed})
	if registrationOpen(t, closed) {
		t.Fatal("closed registration reported open")
	}
	wantProblem(t, closed.client().do(http.MethodPost, "/api/v1/auth/register", registerBody("a@example.test", "closed-a")),
		http.StatusForbidden, "registration_closed")
	if n := closed.ownerCount("SELECT count(*) FROM users"); n != 0 {
		t.Fatalf("users: %d", n)
	}

	open := newEnv(t, envOptions{registration: auth.RegistrationOpen})
	open.register("a@example.test", "open-a")
	open.register("b@example.test", "open-b")
	if !registrationOpen(t, open) {
		t.Fatal("open registration reported closed")
	}
}
