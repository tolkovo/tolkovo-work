package api

import (
	"encoding/base64"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
)

// issueToken issues a token of c and returns its secret.
func issueToken(t *testing.T, c *client, name string, scopes ...string) oapi.AccessTokenCreated {
	t.Helper()
	rec := c.do(http.MethodPost, "/api/v1/me/access-tokens", map[string]any{"name": name, "scopes": scopes, "expires_in_days": 30})
	if rec.Code != http.StatusCreated {
		t.Fatalf("issue token: %d %s", rec.Code, rec.Body)
	}
	return decode[oapi.AccessTokenCreated](t, rec)
}

// A member issues a token and works with tasks by it, as themself, within
// its scopes; the token opens nothing but the tasks API.
func TestAccessTokens_WorkWithTasks(t *testing.T) {
	in := setupInbox(t)
	e, op := in.e, in.op
	opID := in.sop.User.Id

	w := issueToken(t, op, "Claude Code", "tasks:write")
	if !strings.HasPrefix(w.Secret, "tk_pat_") || !strings.HasPrefix(w.Secret, w.Token.Prefix) || len(w.Token.Prefix) != len("tk_pat_")+6 {
		t.Fatalf("secret %q, prefix %q", w.Secret, w.Token.Prefix)
	}
	if got := w.Token.Scopes; len(got) != 2 || got[0] != oapi.ScopeTasksRead || got[1] != oapi.ScopeTasksWrite {
		t.Fatalf("tasks:write must include tasks:read: %v", got)
	}
	if w.Token.UserId != opID || w.Token.ExpiresAt == nil || w.Token.LastUsedAt != nil {
		t.Fatalf("token: %+v", w.Token)
	}
	r := issueToken(t, op, "Скрипт", "tasks:read")

	agent := e.client() // no cookie
	rec := agent.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "Починить вход", "priority": "urgent"}, bearer(w.Secret))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create by token: %d %s", rec.Code, rec.Body)
	}
	task := decode[oapi.Task](t, rec)
	if task.CreatedBy == nil || *task.CreatedBy != opID || task.Priority != oapi.Urgent {
		t.Fatalf("the task is the member's: %+v", task)
	}
	base := "/api/v1/tasks/" + task.Id.String()
	rec = agent.do(http.MethodPut, base, taskUpdate(task, func(b map[string]any) { b["status"] = "in_progress" }), bearer(w.Secret))
	if rec.Code != http.StatusOK || decode[oapi.Task](t, rec).AssigneeId == nil {
		t.Fatalf("take by token: %d %s", rec.Code, rec.Body)
	}
	if rec := agent.do(http.MethodPost, base+"/activity", map[string]any{"body": "Готово в ветке fix/login"}, bearer(w.Secret)); rec.Code != http.StatusCreated {
		t.Fatalf("comment by token: %d %s", rec.Code, rec.Body)
	}
	for _, path := range []string{"/api/v1/tasks", base, base + "/activity", "/api/v1/tasks/summary", "/api/v1/task-queues"} {
		if rec := agent.do(http.MethodGet, path, nil, bearer(r.Secret)); rec.Code != http.StatusOK {
			t.Fatalf("read %s: %d %s", path, rec.Code, rec.Body)
		}
	}

	// A read token does not write.
	wantProblem(t, agent.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "x"}, bearer(r.Secret)), http.StatusForbidden, "forbidden")
	wantProblem(t, agent.do(http.MethodPost, base+"/activity", map[string]any{"body": "x"}, bearer(r.Secret)), http.StatusForbidden, "forbidden")

	// Nothing but the tasks API, whatever the role: no conversations, no
	// profile, no tokens, no deleting, no links to conversations.
	conv := in.conversation(t)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/me"},
		{http.MethodGet, "/api/v1/conversations"},
		{http.MethodGet, "/api/v1/conversations/" + conv.Id.String()},
		{http.MethodGet, "/api/v1/contacts"},
		{http.MethodGet, "/api/v1/me/access-tokens"},
		{http.MethodPost, "/api/v1/me/access-tokens"},
		{http.MethodDelete, base},
		{http.MethodPost, base + "/conversations"},
		{http.MethodGet, "/api/v1/conversations/" + conv.Id.String() + "/tasks"},
	} {
		wantProblem(t, agent.do(c.method, c.path, map[string]any{}, bearer(w.Secret)), http.StatusForbidden, "token_not_allowed")
	}
	wantProblem(t, agent.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "x", "conversation_id": conv.Id}, bearer(w.Secret)),
		http.StatusForbidden, "forbidden")

	// The token is used, and the list shows it without the secret.
	mine := decode[oapi.AccessTokenList](t, op.do(http.MethodGet, "/api/v1/me/access-tokens", nil))
	if len(mine.Items) != 2 || mine.Items[0].Id != r.Token.Id || mine.Items[1].LastUsedAt == nil {
		t.Fatalf("my tokens: %+v", mine.Items)
	}
	if body := op.do(http.MethodGet, "/api/v1/me/access-tokens", nil).Body.String(); strings.Contains(body, w.Secret) {
		t.Fatal("the list returns a secret")
	}
	if n := e.ownerCount("SELECT count(*) FROM access_tokens WHERE prefix = $1 AND token_hash IS NOT NULL", w.Token.Prefix); n != 1 {
		t.Fatalf("stored tokens: %d", n)
	}
}

// Bad, forged, expired and revoked tokens, and tokens of members who left,
// let nobody in; the role is the member's current one.
func TestAccessTokens_Refused(t *testing.T) {
	in := setupInbox(t)
	e, owner, op := in.e, in.owner, in.op
	agent := e.client()
	tok := issueToken(t, op, "agent", "tasks:read")

	rec := agent.do(http.MethodGet, "/api/v1/tasks", nil, bearer("tk_pat_short"))
	wantProblem(t, rec, http.StatusUnauthorized, "invalid_access_token")
	if !strings.HasPrefix(rec.Header().Get("WWW-Authenticate"), "Bearer") {
		t.Fatalf("WWW-Authenticate: %q", rec.Header().Get("WWW-Authenticate"))
	}
	// The same random part naming another workspace finds nothing.
	_, other := e.register("other@example.test", "other")
	forged := forgeWorkspace(t, tok.Secret, other.Workspace.Id)
	for _, bad := range []string{forged, tok.Secret + "x", "tk_ingest_" + strings.TrimPrefix(tok.Secret, "tk_pat_"), ""} {
		wantProblem(t, agent.do(http.MethodGet, "/api/v1/tasks", nil, bearer(bad)), http.StatusUnauthorized, "invalid_access_token")
	}
	wantProblem(t, agent.do(http.MethodGet, "/api/v1/tasks", nil, header("Authorization", "Basic "+tok.Secret)),
		http.StatusUnauthorized, "invalid_access_token")
	// A cookie does not rescue a bad token.
	wantProblem(t, op.do(http.MethodGet, "/api/v1/tasks", nil, bearer("tk_pat_nope")), http.StatusUnauthorized, "invalid_access_token")

	// Expired.
	e.ownerExec("UPDATE access_tokens SET expires_at = now() - interval '1 second' WHERE id = $1", tok.Token.Id)
	wantProblem(t, agent.do(http.MethodGet, "/api/v1/tasks", nil, bearer(tok.Secret)), http.StatusUnauthorized, "invalid_access_token")
	if got := decode[oapi.AccessTokenList](t, op.do(http.MethodGet, "/api/v1/me/access-tokens", nil)); len(got.Items) != 0 {
		t.Fatalf("an expired token is listed: %+v", got.Items)
	}

	// Revoked by its member: at once.
	tok = issueToken(t, op, "agent 2", "tasks:read")
	if rec := agent.do(http.MethodGet, "/api/v1/tasks", nil, bearer(tok.Secret)); rec.Code != http.StatusOK {
		t.Fatalf("live token: %d %s", rec.Code, rec.Body)
	}
	if rec := op.do(http.MethodDelete, "/api/v1/me/access-tokens/"+tok.Token.Id.String(), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, agent.do(http.MethodGet, "/api/v1/tasks", nil, bearer(tok.Secret)), http.StatusUnauthorized, "invalid_access_token")

	// A member who is deactivated, then removed, loses access.
	tok = issueToken(t, op, "agent 3", "tasks:read")
	e.ownerExec("UPDATE memberships SET is_active = false WHERE workspace_id = $1 AND user_id = $2", in.so.Workspace.Id, in.sop.User.Id)
	wantProblem(t, agent.do(http.MethodGet, "/api/v1/tasks", nil, bearer(tok.Secret)), http.StatusUnauthorized, "invalid_access_token")
	e.ownerExec("DELETE FROM memberships WHERE workspace_id = $1 AND user_id = $2", in.so.Workspace.Id, in.sop.User.Id)
	if n := e.ownerCount("SELECT count(*) FROM access_tokens WHERE id = $1", tok.Token.Id); n != 0 {
		t.Fatal("the tokens of a removed member stay")
	}

	// A token of the owner whose role cannot do it stays refused: an admin
	// token reads tasks, and a disabled account loses it.
	adm := issueToken(t, owner, "owner agent", "tasks:read")
	e.ownerExec("UPDATE users SET disabled_at = now() WHERE id = $1", in.so.User.Id)
	wantProblem(t, agent.do(http.MethodGet, "/api/v1/tasks", nil, bearer(adm.Secret)), http.StatusUnauthorized, "invalid_access_token")
}

// Input checks and the limit of tokens per member.
func TestAccessTokens_Validation(t *testing.T) {
	in := setupInbox(t)
	op := in.op
	for _, body := range []map[string]any{
		{"name": "", "scopes": []string{"tasks:read"}, "expires_in_days": nil},
		{"name": strings.Repeat("я", 81), "scopes": []string{"tasks:read"}, "expires_in_days": nil},
		{"name": "x\x00", "scopes": []string{"tasks:read"}, "expires_in_days": nil},
		{"name": "x", "scopes": []string{}, "expires_in_days": nil},
		{"name": "x", "scopes": []string{"conversations:read"}, "expires_in_days": nil},
		{"name": "x", "scopes": []string{"tasks:read"}, "expires_in_days": 7},
		{"name": "x", "scopes": []string{"tasks:read"}, "expires_in_days": nil, "role": "owner"},
	} {
		wantProblem(t, op.do(http.MethodPost, "/api/v1/me/access-tokens", body), http.StatusBadRequest, "invalid_request")
	}
	// Without an end.
	rec := op.do(http.MethodPost, "/api/v1/me/access-tokens", map[string]any{"name": "CI", "scopes": []string{"tasks:read", "tasks:read"}, "expires_in_days": nil})
	if rec.Code != http.StatusCreated {
		t.Fatalf("issue: %d %s", rec.Code, rec.Body)
	}
	if got := decode[oapi.AccessTokenCreated](t, rec).Token; got.ExpiresAt != nil || len(got.Scopes) != 1 {
		t.Fatalf("token: %+v", got)
	}
	for i := 1; i < 20; i++ {
		issueToken(t, op, "t", "tasks:read")
	}
	wantProblem(t, op.do(http.MethodPost, "/api/v1/me/access-tokens", map[string]any{"name": "21", "scopes": []string{"tasks:read"}, "expires_in_days": 30}),
		http.StatusConflict, "too_many_tokens")
	// The limit counts live tokens: an expired one makes room.
	in.e.ownerExec("UPDATE access_tokens SET expires_at = now() - interval '1 second' WHERE name = 'CI'")
	issueToken(t, op, "21", "tasks:read")
}

// Admins see and revoke every member's token; members only their own; a
// workspace sees nothing of another.
func TestAccessTokens_Admin(t *testing.T) {
	in := setupInbox(t)
	e, owner, op := in.e, in.owner, in.op
	opTok := issueToken(t, op, "Курсор оператора", "tasks:write")
	ownTok := issueToken(t, owner, "Мой скрипт", "tasks:read")

	wantProblem(t, op.do(http.MethodGet, "/api/v1/access-tokens", nil), http.StatusForbidden, "forbidden")
	wantProblem(t, op.do(http.MethodDelete, "/api/v1/access-tokens/"+ownTok.Token.Id.String(), nil), http.StatusForbidden, "forbidden")
	// Someone else's token through /me is not found.
	wantProblem(t, op.do(http.MethodDelete, "/api/v1/me/access-tokens/"+ownTok.Token.Id.String(), nil), http.StatusNotFound, "not_found")

	all := decode[oapi.AccessTokenList](t, owner.do(http.MethodGet, "/api/v1/access-tokens", nil))
	if len(all.Items) != 2 {
		t.Fatalf("all tokens: %+v", all.Items)
	}
	i := slices.IndexFunc(all.Items, func(a oapi.AccessToken) bool { return a.Id == opTok.Token.Id })
	if i < 0 || all.Items[i].UserName != "User operator-home" || all.Items[i].UserId != in.sop.User.Id {
		t.Fatalf("operator's token: %+v", all.Items)
	}
	mine := decode[oapi.AccessTokenList](t, owner.do(http.MethodGet, "/api/v1/me/access-tokens", nil))
	if len(mine.Items) != 1 || mine.Items[0].Id != ownTok.Token.Id {
		t.Fatalf("owner's own tokens: %+v", mine.Items)
	}

	// Another workspace's admin.
	stranger, _ := e.register("stranger@example.test", "stranger")
	wantProblem(t, stranger.do(http.MethodDelete, "/api/v1/access-tokens/"+opTok.Token.Id.String(), nil), http.StatusNotFound, "not_found")
	if got := decode[oapi.AccessTokenList](t, stranger.do(http.MethodGet, "/api/v1/access-tokens", nil)); len(got.Items) != 0 {
		t.Fatalf("a stranger sees tokens: %+v", got.Items)
	}
	// And a token of one workspace reads nothing of another.
	sTok := issueToken(t, stranger, "s", "tasks:read")
	task := decode[oapi.Task](t, op.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "Наша задача"}))
	wantProblem(t, e.client().do(http.MethodGet, "/api/v1/tasks/"+task.Id.String(), nil, bearer(sTok.Secret)), http.StatusNotFound, "not_found")

	if rec := owner.do(http.MethodDelete, "/api/v1/access-tokens/"+opTok.Token.Id.String(), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("admin revoke: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, e.client().do(http.MethodGet, "/api/v1/tasks", nil, bearer(opTok.Secret)), http.StatusUnauthorized, "invalid_access_token")
	wantProblem(t, owner.do(http.MethodDelete, "/api/v1/access-tokens/"+uuid.NewString(), nil), http.StatusNotFound, "not_found")
}

// A member invited back starts without their old tokens.
func TestAccessTokens_RejoinDropsTokens(t *testing.T) {
	in := setupInbox(t)
	e := in.e
	tok := issueToken(t, in.op, "agent", "tasks:read")
	e.ownerExec("UPDATE memberships SET is_active = false WHERE workspace_id = $1 AND user_id = $2", in.so.Workspace.Id, in.sop.User.Id)
	rec := in.owner.do(http.MethodPost, "/api/v1/invitations", map[string]any{"email": "operator@example.test", "role": "operator"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite: %d %s", rec.Code, rec.Body)
	}
	token := tokenOf(t, *decode[oapi.InvitationSent](t, rec).InviteUrl)
	if rec := e.client().do(http.MethodPost, "/api/v1/auth/invitation/accept",
		map[string]any{"token": token, "password": "correct horse"}); rec.Code != http.StatusOK {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, e.client().do(http.MethodGet, "/api/v1/tasks", nil, bearer(tok.Secret)), http.StatusUnauthorized, "invalid_access_token")
}

// forgeWorkspace puts another workspace id into a token.
func forgeWorkspace(t *testing.T, secret string, ws uuid.UUID) string {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(secret, "tk_pat_"))
	if err != nil || len(b) != 48 {
		t.Fatalf("token format: %v", err)
	}
	copy(b[32:], ws[:])
	return "tk_pat_" + base64.RawURLEncoding.EncodeToString(b)
}
