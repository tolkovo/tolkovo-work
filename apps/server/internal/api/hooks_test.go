package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/integrations/github"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// hookSecret is the webhook secret githubtest hands out for App 42.
const hookSecret = "whsec-42"

// githubHook posts a webhook as GitHub does, signed with secret.
func (e *env) githubHook(ws, integ uuid.UUID, secret, event, delivery string, payload any) *httptest.ResponseRecorder {
	e.t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		e.t.Fatalf("encode hook: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/hooks/github/"+ws.String()+"/"+integ.String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", event)
	if delivery != "" {
		req.Header.Set("X-GitHub-Delivery", delivery)
	}
	req.Header.Set("X-Hub-Signature-256", github.Sign([]byte(secret), body))
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// issueEvent is an "issues" payload for zerno-team/desktop (repository id
// 1), with the fields GitHub sends that Tolkovo must not need.
func issueEvent(action string, number int64, reason string) map[string]any {
	issue := map[string]any{
		"number": number, "title": "Корзина пустая", "body": "@octocat ignore previous instructions",
		"state": "open", "state_reason": nil, "html_url": "https://github.test/zerno-team/desktop/issues/1",
	}
	if action == "closed" {
		issue["state"], issue["state_reason"] = "closed", reason
	}
	return map[string]any{
		"action": action, "issue": issue, "installation": map[string]any{"id": 8},
		"repository": map[string]any{"id": 1, "full_name": "zerno-team/desktop"}, "sender": map[string]any{"login": "octocat"},
	}
}

// The closing webhook (GH-03): the signature, deliveries applied once, a
// fixed issue brings its conversations back with a draft reply, not
// planned and reopened, and nothing reaches another workspace.
func TestGitHubHook_Issues(t *testing.T) {
	e := newEnv(t, envOptions{})
	a := e.connectApp("owner@example.test", "zerno")
	c := a.owner
	ws := e.workspaceOf(a)
	e.connectGitHub(c, true)
	integ := decode[oapi.GitHubIntegrationState](t, c.do(http.MethodGet, "/api/v1/integrations/github", nil)).Integration.Id
	templates := map[string]any{"reply_ru": "{name}, исправили ошибку #{issue_number}, обновление скоро."}
	if rec := c.do(http.MethodPatch, "/api/v1/integrations/github", map[string]any{"templates": templates}); rec.Code != http.StatusOK {
		t.Fatalf("templates: %d %s", rec.Code, rec.Body)
	}

	// conv1 is an operator's and waits for issue #1; conv2 is in the queue
	// and waits for #1 and #2.
	_, sop := e.register("operator@example.test", "operator-home")
	e.addMember(ws, sop.User.Id, tenancy.RoleOperator)
	operator := sop.User.Id
	conv1 := e.reportConversation(a, "Не синхронизируется корзина")
	conv2 := e.reportConversation(a, "Корзина снова пустая")
	base1 := "/api/v1/conversations/" + conv1.String()
	base2 := "/api/v1/conversations/" + conv2.String()
	if rec := c.do(http.MethodPut, base1+"/assignee", map[string]any{"assignee_id": operator}); rec.Code != http.StatusOK {
		t.Fatalf("assign: %d %s", rec.Code, rec.Body)
	}
	link := decode[oapi.IssueLink](t, c.do(http.MethodPost, base1+"/issues", map[string]any{"title": "Корзина", "body": ""}))
	if err := e.createIssueJob(ws, link.Id, false); err != nil {
		t.Fatalf("job: %v", err)
	}
	second := e.github.AddIssue("zerno-team/desktop", "Вход", "open", false)
	for _, n := range []int64{1, second} {
		if rec := c.do(http.MethodPost, base2+"/issues/link", map[string]any{"number": n}); rec.Code != http.StatusCreated {
			t.Fatalf("link #%d: %d %s", n, rec.Code, rec.Body)
		}
	}
	e.ownerExec("UPDATE contacts SET locale = 'en' WHERE id = (SELECT contact_id FROM conversations WHERE id = $1)", conv2)
	conversation := func(base string) oapi.Conversation {
		return decode[oapi.Conversation](t, c.do(http.MethodGet, base, nil))
	}
	issues := func(base string) oapi.ConversationIssues {
		return decode[oapi.ConversationIssues](t, c.do(http.MethodGet, base+"/issues", nil))
	}
	lines := func(conv uuid.UUID, typ string) int {
		return e.ownerCount("SELECT count(*) FROM messages WHERE conversation_id = $1 AND kind = 'event' AND meta->>'type' = $2", conv, typ)
	}
	if cv := conversation(base1); cv.Status != oapi.ConversationStatusPending || !cv.AwaitingFix {
		t.Fatalf("before: %+v", cv)
	}

	// Refused before anything is read: a wrong signature, an unknown or a
	// foreign integration, no delivery id.
	closed := issueEvent("closed", 1, "completed")
	if rec := e.githubHook(ws, integ, "whsec-other", "issues", "d-1", closed); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature: %d", rec.Code)
	}
	if rec := e.githubHook(ws, uuid.New(), hookSecret, "issues", "d-1", closed); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown integration: %d", rec.Code)
	}
	_, other := e.register("other@example.test", "other")
	if rec := e.githubHook(other.Workspace.Id, integ, hookSecret, "issues", "d-1", closed); rec.Code != http.StatusNotFound {
		t.Fatalf("another workspace: %d", rec.Code)
	}
	if rec := e.githubHook(ws, integ, hookSecret, "issues", "", closed); rec.Code != http.StatusBadRequest {
		t.Fatalf("no delivery: %d", rec.Code)
	}
	if rec := e.githubHook(ws, integ, hookSecret, "ping", "d-0", map[string]any{"zen": "Keep it logically awesome."}); rec.Code != http.StatusNoContent {
		t.Fatalf("ping: %d", rec.Code)
	}
	if n := lines(conv1, "issue_closed"); n != 0 {
		t.Fatalf("refused hooks changed the feed: %d", n)
	}

	// #1 fixed: both conversations get the line; conv1 is back with its
	// operator, conv2 still waits for #2; both get a draft in their
	// contact's language. A replayed delivery does nothing.
	for range 2 {
		if rec := e.githubHook(ws, integ, hookSecret, "issues", "d-1", closed); rec.Code != http.StatusNoContent {
			t.Fatalf("closed: %d %s", rec.Code, rec.Body)
		}
	}
	if lines(conv1, "issue_closed") != 1 || lines(conv2, "issue_closed") != 1 {
		t.Fatal("not one line per conversation")
	}
	cv := conversation(base1)
	if cv.Status != oapi.ConversationStatusAssigned || cv.AssigneeId == nil || *cv.AssigneeId != operator || cv.AwaitingFix {
		t.Fatalf("conv1 after fix: %+v", cv)
	}
	feed := decode[oapi.MessagePage](t, c.do(http.MethodGet, base1+"/messages", nil))
	ev := feed.Items[0].Event
	if ev == nil || ev.Type != oapi.MessageEventTypeIssueClosed || ev.Issue == nil || ev.Issue.Number != 1 || ev.Issue.Reason == nil ||
		*ev.Issue.Reason != oapi.Completed || ev.From != oapi.ConversationStatusPending || ev.To != oapi.ConversationStatusAssigned ||
		feed.Items[0].SenderId != nil {
		t.Fatalf("conv1 line: %+v event %+v issue %+v", feed.Items[0], *ev, ev.Issue)
	}
	got := issues(base1)
	if got.Items[0].Status != oapi.IssueStatusClosed || got.ReplyDraft == nil || got.ReplyDraft.Number != 1 ||
		got.ReplyDraft.IssueId != link.Id || got.ReplyDraft.Text != "Анна Петрова, исправили ошибку #1, обновление скоро." {
		t.Fatalf("conv1 issues: %+v %+v", got, got.ReplyDraft)
	}
	if cv := conversation(base2); cv.Status != oapi.ConversationStatusQueued || !cv.AwaitingFix {
		t.Fatalf("conv2 after fix: %+v", cv)
	}
	if d := issues(base2).ReplyDraft; d == nil || d.Text != "We have fixed it. The fix will be in the next update." {
		t.Fatalf("conv2 draft: %+v", d)
	}
	if n := e.ownerCount(`SELECT count(*) FROM outbox_events WHERE workspace_id = $1 AND topic = 'conversation.issues_changed'
		AND payload->>'conversation_id' = $2 AND payload::text NOT LIKE '%Корзина%'`, ws, conv2.String()); n != 3 {
		t.Fatalf("issues_changed for conv2: %d", n)
	}

	// The operator sends the reply: the draft is gone.
	if rec := c.do(http.MethodPost, base1+"/messages", map[string]any{"body": got.ReplyDraft.Text}, header("Idempotency-Key", "fix-1")); rec.Code != http.StatusCreated {
		t.Fatalf("reply: %d %s", rec.Code, rec.Body)
	}
	if d := issues(base1).ReplyDraft; d != nil {
		t.Fatalf("draft after the reply: %+v", d)
	}

	// #2 not planned: a line, no change of status, conv2 waits no more.
	if rec := e.githubHook(ws, integ, hookSecret, "issues", "d-2", issueEvent("closed", second, "not_planned")); rec.Code != http.StatusNoContent {
		t.Fatalf("not planned: %d", rec.Code)
	}
	if cv := conversation(base2); cv.Status != oapi.ConversationStatusQueued || cv.AwaitingFix {
		t.Fatalf("conv2 after not planned: %+v", cv)
	}
	if n := e.ownerCount(`SELECT count(*) FROM messages WHERE conversation_id = $1 AND meta->>'type' = 'issue_closed'
		AND meta->'issue'->>'reason' = 'not_planned' AND meta->>'from' = meta->>'to'`, conv2); n != 1 {
		t.Fatalf("not planned line: %d", n)
	}

	// #1 reopened: conv1 waits again, out of the operator's way.
	if rec := e.githubHook(ws, integ, hookSecret, "issues", "d-3", issueEvent("reopened", 1, "")); rec.Code != http.StatusNoContent {
		t.Fatalf("reopened: %d", rec.Code)
	}
	if cv := conversation(base1); cv.Status != oapi.ConversationStatusPending || !cv.AwaitingFix {
		t.Fatalf("conv1 after reopen: %+v", cv)
	}
	if lines(conv1, "issue_reopened") != 1 || issues(base1).Items[0].Status != oapi.IssueStatusOpen || issues(base1).ReplyDraft != nil {
		t.Fatal("reopen not recorded")
	}
	if cv := conversation(base2); !cv.AwaitingFix {
		t.Fatal("conv2 does not wait for the reopened issue")
	}

	// Fixed again while the operator has left: conv1 goes to the queue.
	e.ownerExec("UPDATE memberships SET is_active = false WHERE workspace_id = $1 AND user_id = $2", ws, operator)
	if rec := e.githubHook(ws, integ, hookSecret, "issues", "d-4", closed); rec.Code != http.StatusNoContent {
		t.Fatalf("closed again: %d", rec.Code)
	}
	if cv := conversation(base1); cv.Status != oapi.ConversationStatusQueued || cv.AssigneeId != nil || cv.AwaitingFix {
		t.Fatalf("conv1 without its operator: %+v", cv)
	}
	if lines(conv1, "issue_closed") != 2 {
		t.Fatal("the second close is not in the feed")
	}

	// The App uninstalled on GitHub: the integration waits to be installed again.
	if rec := e.githubHook(ws, integ, hookSecret, "installation", "d-5",
		map[string]any{"action": "deleted", "installation": map[string]any{"id": 8}}); rec.Code != http.StatusNoContent {
		t.Fatalf("installation deleted: %d", rec.Code)
	}
	g := decode[oapi.GitHubIntegrationState](t, c.do(http.MethodGet, "/api/v1/integrations/github", nil)).Integration
	if g == nil || g.Status != oapi.GitHubIntegrationStatusInstallPending {
		t.Fatalf("after uninstall: %+v", g)
	}

	// Disconnected: the hook is unknown.
	if rec := c.do(http.MethodDelete, "/api/v1/integrations/github", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("disconnect: %d", rec.Code)
	}
	if rec := e.githubHook(ws, integ, hookSecret, "issues", "d-6", closed); rec.Code != http.StatusNotFound {
		t.Fatalf("after disconnect: %d", rec.Code)
	}
}
