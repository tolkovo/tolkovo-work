package api

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// Authentication is deny-by-default: every /api/v1 route except the public
// ones answers 401 without a session. A new operation in api/openapi.yaml
// is protected without anyone remembering to protect it.
func TestEveryRouteRequiresSessionExceptPublic(t *testing.T) {
	e := newEnv(t, envOptions{withWS: true}) // the WebSocket endpoint too
	routes := 0
	err := chi.Walk(e.h.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/api/v1/") {
			return nil
		}
		routes++
		rec := e.client().do(method, route, map[string]any{})
		if strings.HasPrefix(route, FilesPath+"/") {
			// Signed file links authenticate by signature, not by session.
			if rec.Code != http.StatusForbidden || decode[Problem](t, rec).Code != "link_invalid" {
				t.Errorf("%s %s without a signature: want 403 link_invalid, got %d", method, route, rec.Code)
			}
			return nil
		}
		if publicOperations[method+" "+route] {
			// Login may answer 401 invalid_credentials, never "no session".
			if rec.Code == http.StatusUnauthorized && decode[Problem](t, rec).Code == "unauthenticated" {
				t.Errorf("%s %s is public but demanded a session", method, route)
			}
			return nil
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session: want 401, got %d", method, route, rec.Code)
		}
		return nil
	})
	if err != nil || routes < 8 {
		t.Fatalf("walk: %v, %d routes", err, routes)
	}
}

// Two companies, each with its own owner (docs/00 §11.3).
type twoTenants struct {
	e          *env
	a, b       *client
	sa, sb     oapi.Session
	wsA, wsB   uuid.UUID
	userA, usB uuid.UUID
}

func setupTwoTenants(t *testing.T) twoTenants {
	e := newEnv(t, envOptions{})
	a, sa := e.register("alpha@example.test", "alpha")
	b, sb := e.register("bravo@example.test", "bravo")
	return twoTenants{
		e: e, a: a, b: b, sa: sa, sb: sb,
		wsA: sa.Workspace.Id, wsB: sb.Workspace.Id, userA: sa.User.Id, usB: sb.User.Id,
	}
}

func TestIsolation_MembersOnlyOfOwnWorkspace(t *testing.T) {
	tt := setupTwoTenants(t)
	for _, path := range []string{
		"/api/v1/members",
		// The workspace never comes from the URL: a forged parameter is ignored.
		"/api/v1/members?workspace_id=" + tt.wsB.String(),
	} {
		rec := tt.a.do(http.MethodGet, path, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, rec.Code)
		}
		list := decode[oapi.MemberList](t, rec)
		if len(list.Items) != 1 || list.Items[0].UserId != tt.userA {
			t.Fatalf("%s: A must see only its own member, got %+v", path, list.Items)
		}
	}
}

func TestIsolation_SwitchToForeignWorkspaceIs404(t *testing.T) {
	tt := setupTwoTenants(t)
	for _, id := range []uuid.UUID{tt.wsB, uuid.Must(uuid.NewV7())} {
		rec := tt.a.do(http.MethodPost, "/api/v1/auth/workspace", map[string]any{"workspace_id": id})
		// Same answer for "exists but not yours" and "does not exist".
		wantProblem(t, rec, http.StatusNotFound, "not_found")
	}
	// The failed attempt did not change A's session.
	if me := decode[oapi.Session](t, tt.a.do(http.MethodGet, "/api/v1/me", nil)); me.Workspace.Id != tt.wsA {
		t.Fatalf("session moved to %s", me.Workspace.Id)
	}
}

func TestIsolation_MyWorkspacesListsOnlyMemberships(t *testing.T) {
	tt := setupTwoTenants(t)
	list := decode[oapi.WorkspaceList](t, tt.a.do(http.MethodGet, "/api/v1/me/workspaces", nil))
	if len(list.Items) != 1 || list.Items[0].Id != tt.wsA || list.Items[0].Role != oapi.RoleOwner {
		t.Fatalf("A's workspaces: %+v", list.Items)
	}
}

func TestIsolation_UpdateTouchesOnlyOwnWorkspace(t *testing.T) {
	tt := setupTwoTenants(t)
	// A body naming another workspace is rejected, not silently applied.
	rec := tt.a.do(http.MethodPatch, "/api/v1/workspace", map[string]any{"name": "Hijack", "id": tt.wsB})
	wantProblem(t, rec, http.StatusBadRequest, "invalid_request")

	rec = tt.a.do(http.MethodPatch, "/api/v1/workspace", map[string]any{"name": "Alpha Ltd"})
	if rec.Code != http.StatusOK || decode[oapi.Workspace](t, rec).Id != tt.wsA {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	if me := decode[oapi.Session](t, tt.b.do(http.MethodGet, "/api/v1/me", nil)); me.Workspace.Name != "WS bravo" {
		t.Fatalf("B's workspace changed: %+v", me.Workspace)
	}
}

// Access is re-checked on every request: a session whose membership was
// deactivated stops working at once, and stays revoked.
func TestIsolation_DeactivatedMemberLosesAccess(t *testing.T) {
	tt := setupTwoTenants(t)
	tt.e.addMember(tt.wsA, tt.usB, tenancy.RoleOperator)
	if rec := tt.b.do(http.MethodPost, "/api/v1/auth/workspace", map[string]any{"workspace_id": tt.wsA}); rec.Code != http.StatusOK {
		t.Fatalf("switch: %d %s", rec.Code, rec.Body)
	}
	tt.e.ownerExec("UPDATE memberships SET is_active = false WHERE workspace_id = $1 AND user_id = $2", tt.wsA, tt.usB)
	wantProblem(t, tt.b.do(http.MethodGet, "/api/v1/members", nil), http.StatusUnauthorized, "unauthenticated")

	tt.e.ownerExec("UPDATE memberships SET is_active = true WHERE workspace_id = $1 AND user_id = $2", tt.wsA, tt.usB)
	stale := tt.e.client()
	stale.cookie = tt.b.cookie
	if stale.cookie != nil {
		wantProblem(t, stale.do(http.MethodGet, "/api/v1/me", nil), http.StatusUnauthorized, "unauthenticated")
	}
}

func TestIsolation_DisabledAccountAndDeletedWorkspace(t *testing.T) {
	tt := setupTwoTenants(t)
	tt.e.ownerExec("UPDATE users SET disabled_at = now() WHERE id = $1", tt.userA)
	wantProblem(t, tt.a.do(http.MethodGet, "/api/v1/me", nil), http.StatusUnauthorized, "unauthenticated")

	tt.e.ownerExec("UPDATE workspaces SET deleted_at = now() WHERE id = $1", tt.wsB)
	wantProblem(t, tt.b.do(http.MethodGet, "/api/v1/me", nil), http.StatusUnauthorized, "unauthenticated")
}

// Switching rotates the session: new cookie, new CSRF token, the old token
// is revoked, and the role follows the target workspace.
func TestSwitchWorkspace_RotatesSessionAndRole(t *testing.T) {
	tt := setupTwoTenants(t)
	tt.e.addMember(tt.wsA, tt.usB, tenancy.RoleOperator)
	old := *tt.b.cookie
	oldCSRF := tt.b.csrf

	list := decode[oapi.WorkspaceList](t, tt.b.do(http.MethodGet, "/api/v1/me/workspaces", nil))
	if len(list.Items) != 2 || list.Items[0].Id != tt.wsB || list.Items[1].Id != tt.wsA || list.Items[1].Role != oapi.RoleOperator {
		t.Fatalf("B's workspaces: %+v", list.Items)
	}

	rec := tt.b.do(http.MethodPost, "/api/v1/auth/workspace", map[string]any{"workspace_id": tt.wsA})
	if rec.Code != http.StatusOK {
		t.Fatalf("switch: %d %s", rec.Code, rec.Body)
	}
	s := decode[oapi.Session](t, rec)
	if s.Workspace.Id != tt.wsA || s.Role != oapi.RoleOperator || s.CsrfToken == oldCSRF || tt.b.cookie.Value == old.Value {
		t.Fatalf("unexpected session after switch: %+v", s)
	}
	replay := tt.e.client()
	replay.cookie = &old
	wantProblem(t, replay.do(http.MethodGet, "/api/v1/me", nil), http.StatusUnauthorized, "unauthenticated")

	list2 := decode[oapi.MemberList](t, tt.b.do(http.MethodGet, "/api/v1/members", nil))
	if len(list2.Items) != 2 {
		t.Fatalf("B now sees A's members: want 2, got %+v", list2.Items)
	}
}

// RBAC matrix through HTTP (docs/00 §11.3): every protected endpoint with
// a role that may and a role that may not.
func TestRBAC_MatrixOverHTTP(t *testing.T) {
	e := newEnv(t, envOptions{})
	owner, so := e.register("owner@example.test", "zerno")
	_, sa := e.register("admin@example.test", "admin-home")
	_, sp := e.register("operator@example.test", "operator-home")
	e.addMember(so.Workspace.Id, sa.User.Id, tenancy.RoleAdmin)
	e.addMember(so.Workspace.Id, sp.User.Id, tenancy.RoleOperator)

	clients := map[string]*client{"owner": owner}
	for role, email := range map[string]string{"admin": "admin@example.test", "operator": "operator@example.test"} {
		c, _ := e.login(email)
		if rec := c.do(http.MethodPost, "/api/v1/auth/workspace", map[string]any{"workspace_id": so.Workspace.Id}); rec.Code != http.StatusOK {
			t.Fatalf("%s switch: %d", role, rec.Code)
		}
		clients[role] = c
	}

	tests := []struct {
		method, path string
		body         any
		want         map[string]int
	}{
		{http.MethodGet, "/api/v1/members", nil,
			map[string]int{"owner": 200, "admin": 200, "operator": 200}},
		{http.MethodPatch, "/api/v1/workspace", map[string]any{"timezone": "Europe/Berlin"},
			map[string]int{"owner": 200, "admin": 200, "operator": 403}},
	}
	for _, tc := range tests {
		for role, want := range tc.want {
			t.Run(role+" "+tc.method+" "+tc.path, func(t *testing.T) {
				rec := clients[role].do(tc.method, tc.path, tc.body)
				if rec.Code != want {
					t.Fatalf("want %d, got %d %s", want, rec.Code, rec.Body)
				}
				if want == http.StatusForbidden {
					wantProblem(t, rec, http.StatusForbidden, "forbidden")
				}
			})
		}
	}

	me := decode[oapi.Session](t, clients["operator"].do(http.MethodGet, "/api/v1/me", nil))
	if me.Role != oapi.RoleOperator || !slices.Equal(me.Permissions, []string{
		"contacts.edit", "contacts.view", "conversations.note", "conversations.reply",
		"conversations.tag", "conversations.take", "conversations.view", "issues.create", "members.view",
	}) {
		t.Fatalf("operator /me: %+v", me)
	}
}
