package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
)

// The whole path of a problem report (AP-05, GH-04), through the HTTP API as
// the app, the operator and GitHub use it, with the worker's jobs run in
// place: the app sends a report with a log, the operator reads it and
// answers, the app gets the answer and writes back, the operator files an
// issue, GitHub closes it as fixed, the operator sends the drafted reply and
// the app shows it. The steps' edge cases live in the ingest, issues and
// hook tests; this one checks that they connect.
func TestE2E_ReportToIssueToReply(t *testing.T) {
	e := newEnv(t, envOptions{})
	a := e.connectApp("owner@example.test", "zerno")
	op := a.owner
	ws := e.workspaceOf(a)
	e.connectGitHub(op, true)
	integ := decode[oapi.GitHubIntegrationState](t, op.do(http.MethodGet, "/api/v1/integrations/github", nil)).Integration.Id
	templates := map[string]any{"reply_ru": "{name}, исправили ошибку #{issue_number}, обновление скоро."}
	if rec := op.do(http.MethodPatch, "/api/v1/integrations/github", map[string]any{"templates": templates}); rec.Code != http.StatusOK {
		t.Fatalf("templates: %d %s", rec.Code, rec.Body)
	}

	// 1. The app sends the report: the user's words, facts about the app, a
	// log, and the user signed by the company's backend.
	app := e.appClient("198.51.100.7")
	logFile := reportFile{"zerno.log", "text/plain", []byte("2026-10-05 12:00:01 ERROR sync: cart is empty after login\n")}
	body := report("После входа корзина пустая", logFile)
	body["contact"] = map[string]any{
		"external_id": "user-42", "name": "Анна", "email": "anna@example.test", "identity_token": identityToken(a.identity, "user-42"),
	}
	created := e.submitReport(a, app, body, logFile)
	if !created.ContactVerified {
		t.Fatal("signed contact not verified")
	}
	thread := "/api/v1/ingest/reports/" + created.ReportId.String()
	appAuth := []reqOpt{bearer(a.key), header("X-Report-Token", created.ReportToken)}
	replies := func(since string) oapi.IngestReplies {
		t.Helper()
		// Replies settle for a few seconds before the app sees them; the
		// test moves them back instead of waiting, and lifts the per-report
		// rate between polls.
		e.ownerExec("UPDATE messages SET created_at = created_at - interval '1 minute' WHERE conversation_id IN (SELECT conversation_id FROM app_reports WHERE id = $1)", created.ReportId)
		e.resetRate("ingest:replies:" + created.ReportId.String())
		path := thread + "/replies"
		if since != "" {
			path += "?since=" + since
		}
		rec := app.do(http.MethodGet, path, nil, appAuth...)
		if rec.Code != http.StatusOK {
			t.Fatalf("replies: %d %s", rec.Code, rec.Body)
		}
		return decode[oapi.IngestReplies](t, rec)
	}

	// 2. The worker turns it into a conversation in the queue.
	if err := e.process(e.ingest, ws, created.ReportId, false); err != nil {
		t.Fatalf("process: %v", err)
	}
	inbox := decode[oapi.ConversationPage](t, op.do(http.MethodGet, "/api/v1/conversations?status=queued", nil))
	if len(inbox.Items) != 1 {
		t.Fatalf("inbox: %+v", inbox.Items)
	}
	convID := inbox.Items[0].Id
	conv := "/api/v1/conversations/" + convID.String()

	// 3. The operator takes it, reads the report and downloads the log.
	if rec := op.do(http.MethodPut, conv+"/assignee", map[string]any{"assignee_id": e.ownerID(a)}); rec.Code != http.StatusOK {
		t.Fatalf("take: %d %s", rec.Code, rec.Body)
	}
	feed := decode[oapi.MessagePage](t, op.do(http.MethodGet, conv+"/messages", nil))
	var first *oapi.Message
	for i := range feed.Items {
		if feed.Items[i].Report != nil {
			first = &feed.Items[i]
		}
	}
	if first == nil || first.Body != "После входа корзина пустая" || len(first.Attachments) != 1 {
		t.Fatalf("report message: %+v", feed.Items)
	}
	link := decode[oapi.SignedRequest](t, op.do(http.MethodGet, "/api/v1/attachments/"+first.Attachments[0].Id.String()+"/download-link", nil))
	if got := e.download(link); got != string(logFile.body) {
		t.Fatalf("log: %q", got)
	}

	// 4. The operator answers; the app gets it and the user writes back.
	if rec := op.do(http.MethodPost, conv+"/messages", map[string]any{"body": "Спасибо, Анна! Проверяем."}, header("Idempotency-Key", "e2e-1")); rec.Code != http.StatusCreated {
		t.Fatalf("answer: %d %s", rec.Code, rec.Body)
	}
	got := replies("")
	if len(got.Items) != 1 || got.Items[0].Body != "Спасибо, Анна! Проверяем." || got.Items[0].Sender != oapi.IngestReplySenderOperator {
		t.Fatalf("first replies: %+v", got)
	}
	cursor := got.Cursor
	if rec := app.do(http.MethodPost, thread+"/messages", map[string]any{"message": "Переустановка не помогла"},
		append(appAuth, header("Idempotency-Key", "app-1"))...); rec.Code != http.StatusCreated {
		t.Fatalf("write back: %d %s", rec.Code, rec.Body)
	}
	if !feedHas(t, op, conv, "Переустановка не помогла") {
		t.Fatal("the user's message is not in the conversation")
	}

	// 5. The operator files an issue from the draft; the worker creates it.
	// The issue carries the report, never the user's name or email.
	draft := decode[oapi.IssueDraft](t, op.do(http.MethodGet, conv+"/issues/draft", nil))
	rec := op.do(http.MethodPost, conv+"/issues", map[string]any{"title": "Корзина пустая после входа", "body": draft.Body})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("issue: %d %s", rec.Code, rec.Body)
	}
	issue := decode[oapi.IssueLink](t, rec)
	if err := e.createIssueJob(ws, issue.Id, false); err != nil {
		t.Fatalf("create issue job: %v", err)
	}
	made := e.github.Created()
	if len(made) != 1 || made[0].Repository != "zerno-team/desktop" || made[0].Title != "Корзина пустая после входа" {
		t.Fatalf("created on GitHub: %+v", made)
	}
	for _, private := range []string{"anna@example.test", "Анна"} {
		if strings.Contains(made[0].Body, private) {
			t.Fatalf("the issue carries %q: %s", private, made[0].Body)
		}
	}
	if cv := decode[oapi.Conversation](t, op.do(http.MethodGet, conv, nil)); !cv.AwaitingFix {
		t.Fatalf("conversation does not wait for the fix: %+v", cv)
	}

	// 6. GitHub closes the issue as fixed: the conversation is back with
	// its operator, with a reply drafted from the template.
	if rec := e.githubHook(ws, integ, hookSecret, "issues", "e2e-close", issueEvent("closed", 1, "completed")); rec.Code != http.StatusNoContent {
		t.Fatalf("closed hook: %d %s", rec.Code, rec.Body)
	}
	cv := decode[oapi.Conversation](t, op.do(http.MethodGet, conv, nil))
	if cv.Status != oapi.ConversationStatusAssigned || cv.AwaitingFix {
		t.Fatalf("after the fix: %+v", cv)
	}
	issues := decode[oapi.ConversationIssues](t, op.do(http.MethodGet, conv+"/issues", nil))
	if issues.ReplyDraft == nil || issues.ReplyDraft.Text != "Анна, исправили ошибку #1, обновление скоро." {
		t.Fatalf("reply draft: %+v", issues.ReplyDraft)
	}

	// 7. The operator sends it; the app shows it after the first answer.
	if rec := op.do(http.MethodPost, conv+"/messages", map[string]any{"body": issues.ReplyDraft.Text}, header("Idempotency-Key", "e2e-2")); rec.Code != http.StatusCreated {
		t.Fatalf("fix reply: %d %s", rec.Code, rec.Body)
	}
	got = replies(cursor)
	if len(got.Items) != 1 || got.Items[0].Body != "Анна, исправили ошибку #1, обновление скоро." {
		t.Fatalf("replies after the fix: %+v", got)
	}
}

// feedHas reports whether the conversation's feed holds a message with body.
func feedHas(t *testing.T, c *client, conv, body string) bool {
	t.Helper()
	feed := decode[oapi.MessagePage](t, c.do(http.MethodGet, conv+"/messages", nil))
	for _, m := range feed.Items {
		if m.Body == body {
			return true
		}
	}
	return false
}

// download fetches a signed download link of the fs driver.
func (e *env) download(link oapi.SignedRequest) string {
	e.t.Helper()
	req := httptest.NewRequest(link.Method, link.Url, nil)
	for k, v := range link.Headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("download: %d %s", rec.Code, rec.Body)
	}
	return rec.Body.String()
}
