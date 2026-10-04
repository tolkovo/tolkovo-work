package api

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
)

func TestUpdateMe_ChangesNameAndLanguage(t *testing.T) {
	e := newEnv(t, envOptions{})
	c, s := e.register("marina@example.test", "zerno")

	rec := c.do(http.MethodPatch, "/api/v1/me", map[string]any{"name": "  Марина Ковалёва ", "locale": "en"})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	got := decode[oapi.Session](t, rec)
	if got.User.Name != "Марина Ковалёва" || got.User.Locale != oapi.En || got.User.Email != s.User.Email ||
		got.Workspace.Id != s.Workspace.Id || got.CsrfToken != s.CsrfToken {
		t.Fatalf("unexpected session: %+v", got)
	}
	// The workspace language is a different setting and stays.
	if got.Workspace.Locale != s.Workspace.Locale {
		t.Fatalf("workspace locale changed: %s", got.Workspace.Locale)
	}
	me := decode[oapi.Session](t, c.do(http.MethodGet, "/api/v1/me", nil))
	if me.User.Name != "Марина Ковалёва" || me.User.Locale != oapi.En {
		t.Fatalf("GET /me after patch: %+v", me.User)
	}
	// Absent fields stay as they are.
	if got := decode[oapi.Session](t, c.do(http.MethodPatch, "/api/v1/me", map[string]any{"locale": "ru"})); got.User.Name != "Марина Ковалёва" {
		t.Fatalf("name lost: %+v", got.User)
	}
}

func TestUpdateMe_RejectsInvalidInput(t *testing.T) {
	e := newEnv(t, envOptions{})
	c, _ := e.register("marina@example.test", "zerno")
	for name, body := range map[string]map[string]any{
		"empty name":    {"name": ""},
		"blank name":    {"name": "   "},
		"long name":     {"name": strings.Repeat("я", 121)},
		"bad locale":    {"locale": "de"},
		"email":         {"email": "other@example.test"}, // not changeable here
		"wrong type":    {"name": 42},
		"unknown field": {"workspace_id": uuid.NewString()},
	} {
		t.Run(name, func(t *testing.T) {
			wantProblem(t, c.do(http.MethodPatch, "/api/v1/me", body), http.StatusBadRequest, "invalid_request")
		})
	}
	c.csrf = ""
	wantProblem(t, c.do(http.MethodPatch, "/api/v1/me", map[string]any{"name": "X"}), http.StatusForbidden, "csrf_failed")
}

// Personal settings need a session, not a permission: an operator changes
// their own profile and password.
func TestPersonalSettings_OpenToOperator(t *testing.T) {
	e := newEnv(t, envOptions{})
	_, owner := e.register("marina@example.test", "zerno")
	op, opSession := e.register("oleg@example.test", "oleg")
	e.addMember(owner.Workspace.Id, opSession.User.Id, "operator")
	rec := op.do(http.MethodPost, "/api/v1/auth/workspace", map[string]any{"workspace_id": owner.Workspace.Id})
	if s := decode[oapi.Session](t, rec); rec.Code != http.StatusOK || s.Role != oapi.RoleOperator {
		t.Fatalf("switch: %d %s", rec.Code, rec.Body)
	}

	if rec := op.do(http.MethodPatch, "/api/v1/me", map[string]any{"name": "Олег"}); rec.Code != http.StatusOK {
		t.Fatalf("operator profile: %d %s", rec.Code, rec.Body)
	}
	if rec := op.do(http.MethodGet, "/api/v1/me/sessions", nil); rec.Code != http.StatusOK {
		t.Fatalf("operator sessions: %d %s", rec.Code, rec.Body)
	}
	rec = op.do(http.MethodPost, "/api/v1/me/password", map[string]any{"current_password": "correct horse", "new_password": "battery staple"})
	if rec.Code != http.StatusOK {
		t.Fatalf("operator password: %d %s", rec.Code, rec.Body)
	}
	// The user is global: the new name shows in every workspace.
	members := decode[oapi.MemberList](t, op.do(http.MethodGet, "/api/v1/members", nil))
	found := false
	for _, m := range members.Items {
		found = found || (m.UserId == opSession.User.Id && m.Name == "Олег")
	}
	if !found {
		t.Fatalf("members: %+v", members.Items)
	}
}

func TestChangePassword_RevokesOtherSessionsAndRotatesCurrent(t *testing.T) {
	e := newEnv(t, envOptions{})
	c, first := e.register("marina@example.test", "zerno")
	laptop, _ := e.login("marina@example.test")
	oldCookie := *c.cookie

	rec := c.do(http.MethodPost, "/api/v1/me/password", map[string]any{
		"current_password": "correct horse", "new_password": "battery staple",
	}, header("User-Agent", "Firefox/131"))
	if rec.Code != http.StatusOK {
		t.Fatalf("change: %d %s", rec.Code, rec.Body)
	}
	s := decode[oapi.Session](t, rec)
	if c.cookie == nil || c.cookie.Value == oldCookie.Value || s.CsrfToken == first.CsrfToken || !c.cookie.HttpOnly {
		t.Fatal("the current session must get a new token and CSRF token")
	}
	if strings.Contains(rec.Body.String(), "battery") || strings.Contains(rec.Body.String(), "correct horse") {
		t.Fatal("a password was echoed back")
	}

	// The new session works, the old token and the other device do not.
	if rec := c.do(http.MethodGet, "/api/v1/me", nil); rec.Code != http.StatusOK {
		t.Fatalf("new session: %d", rec.Code)
	}
	replay := e.client()
	replay.cookie = &oldCookie
	wantProblem(t, replay.do(http.MethodGet, "/api/v1/me", nil), http.StatusUnauthorized, "unauthenticated")
	wantProblem(t, laptop.do(http.MethodGet, "/api/v1/me", nil), http.StatusUnauthorized, "unauthenticated")
	list := decode[oapi.DeviceSessionList](t, c.do(http.MethodGet, "/api/v1/me/sessions", nil))
	if len(list.Items) != 1 || !list.Items[0].Current || list.Items[0].UserAgent != "Firefox/131" {
		t.Fatalf("sessions after the change: %+v", list.Items)
	}

	// The old password stops working, the new one signs in.
	wantProblem(t, e.client().do(http.MethodPost, "/api/v1/auth/login",
		map[string]any{"email": "marina@example.test", "password": "correct horse"}), http.StatusUnauthorized, "invalid_credentials")
	if rec := e.client().do(http.MethodPost, "/api/v1/auth/login",
		map[string]any{"email": "marina@example.test", "password": "battery staple"}); rec.Code != http.StatusOK {
		t.Fatalf("login with the new password: %d", rec.Code)
	}
}

func TestChangePassword_WrongCurrentPasswordChangesNothing(t *testing.T) {
	e := newEnv(t, envOptions{})
	c, s := e.register("marina@example.test", "zerno")
	laptop, _ := e.login("marina@example.test")

	rec := c.do(http.MethodPost, "/api/v1/me/password", map[string]any{"current_password": "wrong horse", "new_password": "battery staple"})
	wantProblem(t, rec, http.StatusUnprocessableEntity, "wrong_password")
	if len(rec.Result().Cookies()) != 0 || c.csrf != s.CsrfToken {
		t.Fatal("a failed change must not touch the session")
	}
	if rec := laptop.do(http.MethodGet, "/api/v1/me", nil); rec.Code != http.StatusOK {
		t.Fatalf("other sessions must survive a failed change: %d", rec.Code)
	}
	e.login("marina@example.test") // the old password still works

	for name, body := range map[string]map[string]any{
		"short new password": {"current_password": "correct horse", "new_password": "1234567"},
		"long new password":  {"current_password": "correct horse", "new_password": strings.Repeat("x", 129)},
	} {
		t.Run(name, func(t *testing.T) {
			wantProblem(t, c.do(http.MethodPost, "/api/v1/me/password", body), http.StatusBadRequest, "invalid_request")
		})
	}
	// An absent current password is simply a wrong one.
	wantProblem(t, c.do(http.MethodPost, "/api/v1/me/password", map[string]any{"new_password": "battery staple"}),
		http.StatusUnprocessableEntity, "wrong_password")
}

func TestChangePassword_RateLimitedPerUser(t *testing.T) {
	e := newEnv(t, envOptions{})
	c, _ := e.register("marina@example.test", "zerno")
	other, _ := e.register("oleg@example.test", "oleg")
	for i := range 10 {
		rec := c.do(http.MethodPost, "/api/v1/me/password", map[string]any{
			"current_password": "guess-" + strconv.Itoa(i), "new_password": "battery staple",
		})
		wantProblem(t, rec, http.StatusUnprocessableEntity, "wrong_password")
	}
	// Even the right password is refused while the limit holds.
	rec := c.do(http.MethodPost, "/api/v1/me/password", map[string]any{"current_password": "correct horse", "new_password": "battery staple"})
	wantProblem(t, rec, http.StatusTooManyRequests, "too_many_requests")
	if ra, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || ra < 1 || ra > 3600 {
		t.Fatalf("Retry-After: %q", rec.Header().Get("Retry-After"))
	}
	// The limit is per user.
	if rec := other.do(http.MethodPost, "/api/v1/me/password", map[string]any{
		"current_password": "correct horse", "new_password": "battery staple",
	}); rec.Code != http.StatusOK {
		t.Fatalf("another user: %d %s", rec.Code, rec.Body)
	}
}

func TestMySessions_ListAndRevokeOne(t *testing.T) {
	e := newEnv(t, envOptions{})
	c := e.client()
	rec := c.do(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"email": "marina@example.test", "password": "correct horse", "name": "Marina",
		"workspace_name": "Zerno", "workspace_slug": "zerno",
	}, header("User-Agent", "Firefox/131"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: %d", rec.Code)
	}
	phone := e.client()
	phone.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "marina@example.test", "password": "correct horse"},
		header("User-Agent", "Mobile Safari "+strings.Repeat("x", 300)))

	rec = c.do(http.MethodGet, "/api/v1/me/sessions", nil)
	list := decode[oapi.DeviceSessionList](t, rec)
	if len(list.Items) != 2 || !list.Items[0].Current || list.Items[1].Current ||
		list.Items[0].UserAgent != "Firefox/131" || len(list.Items[1].UserAgent) != 200 ||
		!strings.HasPrefix(list.Items[1].UserAgent, "Mobile Safari") {
		t.Fatalf("sessions: %+v", list.Items)
	}
	for _, it := range list.Items {
		if it.CreatedAt.IsZero() || it.LastSeenAt.Before(it.CreatedAt) || it.Id == uuid.Nil {
			t.Fatalf("session fields: %+v", it)
		}
	}
	// Neither token appears in the answer.
	if strings.Contains(rec.Body.String(), c.cookie.Value) || strings.Contains(rec.Body.String(), phone.cookie.Value) {
		t.Fatal("a session token leaked into the list")
	}

	if rec := c.do(http.MethodDelete, "/api/v1/me/sessions/"+list.Items[1].Id.String(), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, phone.do(http.MethodGet, "/api/v1/me", nil), http.StatusUnauthorized, "unauthenticated")
	wantProblem(t, c.do(http.MethodDelete, "/api/v1/me/sessions/"+list.Items[1].Id.String(), nil), http.StatusNotFound, "not_found")

	// The current session ends by logout, not here.
	wantProblem(t, c.do(http.MethodDelete, "/api/v1/me/sessions/"+list.Items[0].Id.String(), nil), http.StatusConflict, "current_session")
	wantProblem(t, c.do(http.MethodDelete, "/api/v1/me/sessions/"+uuid.NewString(), nil), http.StatusNotFound, "not_found")
	wantProblem(t, c.do(http.MethodDelete, "/api/v1/me/sessions/not-a-uuid", nil), http.StatusNotFound, "not_found")
	c.csrf = ""
	wantProblem(t, c.do(http.MethodDelete, "/api/v1/me/sessions/"+uuid.NewString(), nil), http.StatusForbidden, "csrf_failed")
}

// Another user's session id answers 404, as if it did not exist.
func TestIsolation_SessionsOnlyOfOwnAccount(t *testing.T) {
	tt := setupTwoTenants(t)
	bList := decode[oapi.DeviceSessionList](t, tt.b.do(http.MethodGet, "/api/v1/me/sessions", nil))
	if len(bList.Items) != 1 {
		t.Fatalf("bravo's sessions: %+v", bList.Items)
	}
	aList := decode[oapi.DeviceSessionList](t, tt.a.do(http.MethodGet, "/api/v1/me/sessions", nil))
	if len(aList.Items) != 1 || aList.Items[0].Id == bList.Items[0].Id {
		t.Fatalf("alpha sees: %+v", aList.Items)
	}
	wantProblem(t, tt.a.do(http.MethodDelete, "/api/v1/me/sessions/"+bList.Items[0].Id.String(), nil), http.StatusNotFound, "not_found")
	if rec := tt.b.do(http.MethodGet, "/api/v1/me", nil); rec.Code != http.StatusOK {
		t.Fatalf("bravo's session must survive: %d", rec.Code)
	}
	// "Sign out on other devices" of alpha leaves bravo alone.
	if rec := tt.a.do(http.MethodPost, "/api/v1/me/sessions/revoke-others", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke others: %d", rec.Code)
	}
	if rec := tt.b.do(http.MethodGet, "/api/v1/me", nil); rec.Code != http.StatusOK {
		t.Fatalf("bravo after alpha's revoke-others: %d", rec.Code)
	}
}

func TestRevokeOtherSessions(t *testing.T) {
	e := newEnv(t, envOptions{})
	c, s := e.register("marina@example.test", "zerno")
	laptop, _ := e.login("marina@example.test")
	phone, _ := e.login("marina@example.test")

	if rec := c.do(http.MethodPost, "/api/v1/me/sessions/revoke-others", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke others: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, laptop.do(http.MethodGet, "/api/v1/me", nil), http.StatusUnauthorized, "unauthenticated")
	wantProblem(t, phone.do(http.MethodGet, "/api/v1/me", nil), http.StatusUnauthorized, "unauthenticated")
	me := c.do(http.MethodGet, "/api/v1/me", nil)
	if me.Code != http.StatusOK || decode[oapi.Session](t, me).CsrfToken != s.CsrfToken {
		t.Fatal("the current session must stay as it is")
	}
	if list := decode[oapi.DeviceSessionList](t, c.do(http.MethodGet, "/api/v1/me/sessions", nil)); len(list.Items) != 1 {
		t.Fatalf("sessions left: %+v", list.Items)
	}
}

func TestPersonalRoutes_RequireSession(t *testing.T) {
	e := newEnv(t, envOptions{})
	for _, r := range []struct{ method, path string }{
		{http.MethodPatch, "/api/v1/me"},
		{http.MethodPost, "/api/v1/me/password"},
		{http.MethodGet, "/api/v1/me/sessions"},
		{http.MethodPost, "/api/v1/me/sessions/revoke-others"},
		{http.MethodDelete, "/api/v1/me/sessions/" + uuid.NewString()},
		{http.MethodDelete, "/api/v1/me/sessions/not-a-uuid"},
	} {
		wantProblem(t, e.client().do(r.method, r.path, map[string]any{}), http.StatusUnauthorized, "unauthenticated")
	}
}
