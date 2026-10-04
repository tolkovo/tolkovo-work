package api

import (
	"encoding/json"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"testing"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
)

func TestRegister_CreatesOwnerAndSignsIn(t *testing.T) {
	e := newEnv(t, envOptions{})
	c, s := e.register("marina@example.test", "zerno")

	if s.Role != oapi.RoleOwner || s.Workspace.Slug != "zerno" || s.User.Email != "marina@example.test" {
		t.Fatalf("unexpected session: %+v", s)
	}
	if !slices.Equal(s.Permissions, []string{
		"contacts.edit", "contacts.view", "conversations.assign", "conversations.create", "conversations.manage",
		"conversations.note", "conversations.reply", "conversations.tag", "conversations.take", "conversations.view",
		"members.manage", "members.view", "tags.manage", "workspace.delete", "workspace.manage",
	}) {
		t.Fatalf("owner permissions: %v", s.Permissions)
	}
	if c.cookie == nil || !c.cookie.HttpOnly || c.cookie.SameSite != http.SameSiteLaxMode || c.cookie.Secure ||
		c.cookie.Path != "/" || c.cookie.Name != cookieName {
		t.Fatalf("unexpected development cookie: %+v", c.cookie)
	}
	me := c.do(http.MethodGet, "/api/v1/me", nil)
	if me.Code != http.StatusOK || decode[oapi.Session](t, me).Workspace.Id != s.Workspace.Id {
		t.Fatalf("me: %d %s", me.Code, me.Body)
	}
	if me.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("session responses must not be cached")
	}
}

func TestRegister_SecureCookieOutsideDevelopment(t *testing.T) {
	e := newEnv(t, envOptions{secure: true})
	c, _ := e.register("marina@example.test", "zerno")
	if c.cookie == nil || c.cookie.Name != secureCookieName || !c.cookie.Secure || !c.cookie.HttpOnly ||
		c.cookie.SameSite != http.SameSiteLaxMode || c.cookie.Domain != "" {
		t.Fatalf("unexpected production cookie: %+v", c.cookie)
	}
	if rec := c.do(http.MethodGet, "/api/v1/me", nil); rec.Code != http.StatusOK {
		t.Fatalf("me with secure cookie: %d", rec.Code)
	}
}

func TestRegister_Conflicts(t *testing.T) {
	e := newEnv(t, envOptions{})
	e.register("marina@example.test", "zerno")
	body := func(email, slug string) map[string]any {
		return map[string]any{
			"email": email, "password": "correct horse", "name": "X",
			"workspace_name": "X", "workspace_slug": slug,
		}
	}
	c := e.client()
	wantProblem(t, c.do(http.MethodPost, "/api/v1/auth/register", body("MARINA@example.test", "other")), http.StatusConflict, "email_taken")
	wantProblem(t, c.do(http.MethodPost, "/api/v1/auth/register", body("oleg@example.test", "zerno")), http.StatusConflict, "slug_taken")

	// The failed registration of oleg left no account behind (atomicity).
	wantProblem(t, c.do(http.MethodPost, "/api/v1/auth/login",
		map[string]any{"email": "oleg@example.test", "password": "correct horse"}), http.StatusUnauthorized, "invalid_credentials")
}

func TestRegister_RejectsInvalidInput(t *testing.T) {
	e := newEnv(t, envOptions{})
	valid := map[string]any{
		"email": "a@example.test", "password": "correct horse", "name": "A",
		"workspace_name": "A", "workspace_slug": "alpha",
	}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for kk, vv := range valid {
			m[kk] = vv
		}
		m[k] = v
		return m
	}
	tests := map[string]any{
		"short password":          with("password", "1234567"),
		"bad email":               with("email", "not-an-email"),
		"email with name":         with("email", "A <a@example.test>"),
		"bad slug":                with("workspace_slug", "Alpha!"),
		"bad timezone":            with("timezone", "Mars/Base"),
		"bad locale":              with("locale", "de"),
		"unknown field":           with("workspace_id", "00000000-0000-0000-0000-000000000000"),
		"array instead of object": []any{valid},
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			wantProblem(t, e.client().do(http.MethodPost, "/api/v1/auth/register", body), http.StatusBadRequest, "invalid_request")
		})
	}
	t.Run("not json", func(t *testing.T) {
		rec := e.client().do(http.MethodPost, "/api/v1/auth/register", valid, header("Content-Type", "text/plain"))
		wantProblem(t, rec, http.StatusBadRequest, "invalid_request")
	})
}

func TestRegister_RateLimitedPerIP(t *testing.T) {
	e := newEnv(t, envOptions{})
	c := e.client()
	var last int
	for i := range 11 {
		rec := c.do(http.MethodPost, "/api/v1/auth/register", map[string]any{
			"email": "u" + strconv.Itoa(i) + "@example.test", "password": "short", "name": "U",
			"workspace_name": "U", "workspace_slug": "slug-" + strconv.Itoa(i),
		})
		last = rec.Code
		if i < 10 && rec.Code != http.StatusBadRequest {
			t.Fatalf("attempt %d: want 400, got %d", i+1, rec.Code)
		}
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("11th registration from one IP: want 429, got %d", last)
	}
}

// Behind a trusted proxy the limit counts the client from X-Forwarded-For,
// so one visitor running out does not lock out the others.
func TestRegister_RateLimitedPerClientBehindProxy(t *testing.T) {
	e := newEnv(t, envOptions{proxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}})
	attempt := func(i int, client string) int {
		c := e.client()
		c.ip = "10.0.0.2"
		return c.do(http.MethodPost, "/api/v1/auth/register", map[string]any{
			"email": "u" + strconv.Itoa(i) + "@example.test", "password": "short", "name": "U",
			"workspace_name": "U", "workspace_slug": "slug-" + strconv.Itoa(i),
		}, header("X-Forwarded-For", client)).Code
	}
	for i := range 10 {
		if code := attempt(i, "203.0.113.7"); code != http.StatusBadRequest {
			t.Fatalf("attempt %d: want 400, got %d", i+1, code)
		}
	}
	if code := attempt(10, "203.0.113.7"); code != http.StatusTooManyRequests {
		t.Fatalf("11th attempt of one client: want 429, got %d", code)
	}
	if code := attempt(11, "198.51.100.9"); code != http.StatusBadRequest {
		t.Fatalf("another client behind the same proxy: want 400, got %d", code)
	}
}

// The answers for an unknown email, a wrong password and a disabled account
// are identical, so login does not reveal which emails are registered.
func TestLogin_DoesNotRevealWhetherEmailExists(t *testing.T) {
	e := newEnv(t, envOptions{})
	_, s := e.register("marina@example.test", "zerno")
	e.register("blocked@example.test", "blocked")
	e.ownerExec("UPDATE users SET disabled_at = now() WHERE email = 'blocked@example.test'")

	attempts := map[string]map[string]any{
		"unknown email":  {"email": "nobody@example.test", "password": "correct horse"},
		"wrong password": {"email": "marina@example.test", "password": "wrong horse"},
		"disabled":       {"email": "blocked@example.test", "password": "correct horse"},
	}
	var bodies []Problem
	for name, body := range attempts {
		rec := e.client().do(http.MethodPost, "/api/v1/auth/login", body)
		wantProblem(t, rec, http.StatusUnauthorized, "invalid_credentials")
		if len(rec.Result().Cookies()) != 0 {
			t.Fatalf("%s: no cookie may be set", name)
		}
		p := decode[Problem](t, rec)
		p.RequestID = ""
		bodies = append(bodies, p)
	}
	if bodies[0] != bodies[1] || bodies[1] != bodies[2] {
		t.Fatalf("answers differ: %+v", bodies)
	}

	c, got := e.login("MARINA@example.test") // email lookup ignores case
	if got.User.Id != s.User.Id || c.cookie == nil {
		t.Fatalf("login: %+v", got)
	}
}

func TestLogin_RateLimitedPerEmailAndIP(t *testing.T) {
	e := newEnv(t, envOptions{})
	e.register("marina@example.test", "zerno")
	c := e.client()
	for i := range 10 {
		rec := c.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "marina@example.test", "password": "guess-" + strconv.Itoa(i)})
		wantProblem(t, rec, http.StatusUnauthorized, "invalid_credentials")
	}
	// Even the right password is refused while the limit holds.
	rec := c.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "marina@example.test", "password": "correct horse"})
	wantProblem(t, rec, http.StatusTooManyRequests, "too_many_requests")
	if ra, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || ra < 1 || ra > 60 {
		t.Fatalf("Retry-After: %q", rec.Header().Get("Retry-After"))
	}
	// Another IP is limited separately for this email (until the per-email limit).
	other := e.client()
	other.ip = "198.51.100.7"
	if rec := other.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "marina@example.test", "password": "correct horse"}); rec.Code != http.StatusOK {
		t.Fatalf("other IP: want 200, got %d", rec.Code)
	}
}

func TestLogout_RevokesSession(t *testing.T) {
	e := newEnv(t, envOptions{})
	c, _ := e.register("marina@example.test", "zerno")
	stolen := *c.cookie

	if rec := c.do(http.MethodPost, "/api/v1/auth/logout", nil); rec.Code != http.StatusNoContent || c.cookie != nil {
		t.Fatalf("logout: %d, cookie %+v", rec.Code, c.cookie)
	}
	replay := e.client()
	replay.cookie = &stolen
	wantProblem(t, replay.do(http.MethodGet, "/api/v1/me", nil), http.StatusUnauthorized, "unauthenticated")
}

// Signing in again rotates the session: the token the browser had before
// stops working (no session fixation, no lingering tokens).
func TestLogin_RotatesExistingSession(t *testing.T) {
	e := newEnv(t, envOptions{})
	c, first := e.register("marina@example.test", "zerno")
	old := *c.cookie

	rec := c.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "marina@example.test", "password": "correct horse"})
	if rec.Code != http.StatusOK || c.cookie.Value == old.Value || c.csrf == first.CsrfToken {
		t.Fatalf("login must issue a new session and CSRF token: %d", rec.Code)
	}
	replay := e.client()
	replay.cookie = &old
	wantProblem(t, replay.do(http.MethodGet, "/api/v1/me", nil), http.StatusUnauthorized, "unauthenticated")
}

func TestLogin_NoWorkspace(t *testing.T) {
	e := newEnv(t, envOptions{})
	e.register("marina@example.test", "zerno")
	e.ownerExec("UPDATE memberships SET is_active = false")
	rec := e.client().do(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "marina@example.test", "password": "correct horse"})
	wantProblem(t, rec, http.StatusForbidden, "no_workspace")
}

func TestCSRF_RequiredOnUnsafeMethods(t *testing.T) {
	e := newEnv(t, envOptions{})
	c, s := e.register("marina@example.test", "zerno")
	patch := map[string]any{"name": "Renamed"}

	c.csrf = ""
	wantProblem(t, c.do(http.MethodPatch, "/api/v1/workspace", patch), http.StatusForbidden, "csrf_failed")
	c.csrf = s.CsrfToken + "x"
	wantProblem(t, c.do(http.MethodPatch, "/api/v1/workspace", patch), http.StatusForbidden, "csrf_failed")
	wantProblem(t, c.do(http.MethodPost, "/api/v1/auth/logout", nil), http.StatusForbidden, "csrf_failed")

	// Safe methods need no token.
	if rec := c.do(http.MethodGet, "/api/v1/me", nil); rec.Code != http.StatusOK {
		t.Fatalf("GET without CSRF token: %d", rec.Code)
	}
	c.csrf = s.CsrfToken
	if rec := c.do(http.MethodPatch, "/api/v1/workspace", patch); rec.Code != http.StatusOK {
		t.Fatalf("with token: %d %s", rec.Code, rec.Body)
	}
}

func TestSameOrigin_RejectsCrossOriginUnsafeRequests(t *testing.T) {
	e := newEnv(t, envOptions{trusted: []string{"https://panel.example.com"}})
	e.register("marina@example.test", "zerno")
	login := map[string]any{"email": "marina@example.test", "password": "correct horse"}

	tests := []struct {
		name string
		opts []reqOpt
		want int
	}{
		{"foreign origin", []reqOpt{header("Origin", "https://evil.example")}, http.StatusForbidden},
		{"null origin", []reqOpt{header("Origin", "null")}, http.StatusForbidden},
		{"cross-site fetch without origin", []reqOpt{header("Sec-Fetch-Site", "cross-site")}, http.StatusForbidden},
		{"same origin", []reqOpt{header("Origin", "http://example.com")}, http.StatusOK}, // httptest Host
		{"trusted origin", []reqOpt{header("Origin", "https://panel.example.com")}, http.StatusOK},
		{"non-browser client", nil, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := e.client().do(http.MethodPost, "/api/v1/auth/login", login, tt.opts...)
			if rec.Code != tt.want {
				t.Fatalf("want %d, got %d %s", tt.want, rec.Code, rec.Body)
			}
			if tt.want == http.StatusForbidden {
				wantProblem(t, rec, http.StatusForbidden, "cross_origin")
			}
		})
	}
}

// The CSRF token is a secret of the session and must not leak into a
// cookie readable by scripts or into any other response than Session.
func TestSession_OnlyHttpOnlyCookie(t *testing.T) {
	e := newEnv(t, envOptions{})
	c := e.client()
	rec := c.do(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"email": "a@example.test", "password": "correct horse", "name": "A",
		"workspace_name": "A", "workspace_slug": "alpha",
	})
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly {
		t.Fatalf("want exactly one HttpOnly cookie, got %+v", cookies)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["password"]; ok {
		t.Fatal("password echoed back")
	}
}
