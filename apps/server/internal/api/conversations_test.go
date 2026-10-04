package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// inbox is a workspace with an owner and an operator signed in to it.
type inbox struct {
	e         *env
	owner, op *client
	so, sop   oapi.Session
}

func setupInbox(t *testing.T) inbox {
	t.Helper()
	e := newEnv(t, envOptions{})
	owner, so := e.register("owner@example.test", "zerno")
	_, sop := e.register("operator@example.test", "operator-home")
	e.addMember(so.Workspace.Id, sop.User.Id, tenancy.RoleOperator)
	op, _ := e.login("operator@example.test")
	if rec := op.do(http.MethodPost, "/api/v1/auth/workspace", map[string]any{"workspace_id": so.Workspace.Id}); rec.Code != http.StatusOK {
		t.Fatalf("operator switch: %d %s", rec.Code, rec.Body)
	}
	return inbox{e: e, owner: owner, op: op, so: so, sop: sop}
}

func (in inbox) conversation(t *testing.T) oapi.Conversation {
	t.Helper()
	rec := in.owner.do(http.MethodPost, "/api/v1/contacts", map[string]any{"name": "Ivan", "email": "ivan@example.test"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create contact: %d %s", rec.Code, rec.Body)
	}
	contact := decode[oapi.Contact](t, rec)
	rec = in.owner.do(http.MethodPost, "/api/v1/conversations", map[string]any{"contact_id": contact.Id, "subject": "Refund"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create conversation: %d %s", rec.Code, rec.Body)
	}
	return decode[oapi.Conversation](t, rec)
}

func TestConversationsAPI_OperatorFlow(t *testing.T) {
	in := setupInbox(t)
	c := in.conversation(t)
	if c.Status != "queued" || c.Number != 1 || c.Contact.Name != "Ivan" || c.AssigneeId != nil {
		t.Fatalf("created: %+v", c)
	}
	base := "/api/v1/conversations/" + c.Id.String()

	// The operator cannot reply before taking it.
	wantProblem(t, in.op.do(http.MethodPost, base+"/messages", map[string]any{"body": "hi"}), http.StatusForbidden, "not_assignee")
	// Taking it requires the assignee field, even if null.
	wantProblem(t, in.op.do(http.MethodPut, base+"/assignee", map[string]any{}), http.StatusBadRequest, "invalid_request")
	rec := in.op.do(http.MethodPut, base+"/assignee", map[string]any{"assignee_id": in.sop.User.Id, "version": c.Version})
	if rec.Code != http.StatusOK {
		t.Fatalf("take: %d %s", rec.Code, rec.Body)
	}
	c = decode[oapi.Conversation](t, rec)
	if c.Status != "assigned" || *c.AssigneeId != in.sop.User.Id {
		t.Fatalf("taken: %+v", c)
	}

	// Reply with a file, retried with the same Idempotency-Key.
	body := []byte("trace")
	sum := sha256.Sum256(body)
	rec = in.op.do(http.MethodPost, base+"/attachments", map[string]any{
		"file_name": "trace.txt", "content_type": "text/plain", "size": len(body), "sha256": hex.EncodeToString(sum[:]),
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	up := decode[oapi.AttachmentUpload](t, rec)
	in.putFile(t, up.Upload, body)
	msg := map[string]any{"body": "See the file", "attachment_ids": []uuid.UUID{up.Attachment.Id}}
	rec = in.op.do(http.MethodPost, base+"/messages", msg, header("Idempotency-Key", "k1"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("reply: %d %s", rec.Code, rec.Body)
	}
	m := decode[oapi.Message](t, rec)
	if len(m.Attachments) != 1 || m.Attachments[0].Status != "ready" || m.Kind != "text" {
		t.Fatalf("reply: %+v", m)
	}
	rec = in.op.do(http.MethodPost, base+"/messages", msg, header("Idempotency-Key", "k1"))
	if rec.Code != http.StatusOK || decode[oapi.Message](t, rec).Id != m.Id {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body)
	}
	msg["body"] = "other"
	wantProblem(t, in.op.do(http.MethodPost, base+"/messages", msg, header("Idempotency-Key", "k1")),
		http.StatusUnprocessableEntity, "idempotency_key_reused")

	rec = in.op.do(http.MethodGet, "/api/v1/attachments/"+up.Attachment.Id.String()+"/download-link", nil)
	if rec.Code != http.StatusOK || decode[oapi.SignedRequest](t, rec).Method != http.MethodGet {
		t.Fatalf("download link: %d %s", rec.Code, rec.Body)
	}

	// State machine over HTTP.
	rec = in.op.do(http.MethodPost, base+"/status", map[string]any{"status": "resolved"})
	if rec.Code != http.StatusOK || decode[oapi.Conversation](t, rec).ResolvedAt == nil {
		t.Fatalf("resolve: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, in.op.do(http.MethodPost, base+"/status", map[string]any{"status": "pending"}), http.StatusConflict, "invalid_transition")
	wantProblem(t, in.op.do(http.MethodPost, base+"/status", map[string]any{"status": "closed", "version": 1}), http.StatusConflict, "version_conflict")

	list := decode[oapi.MessagePage](t, in.op.do(http.MethodGet, base+"/messages", nil))
	if len(list.Items) != 1 || list.NextCursor != nil {
		t.Fatalf("feed: %+v", list)
	}
}

// putFile uploads through the fs driver's signed link, as a browser would.
func (in inbox) putFile(t *testing.T, up oapi.SignedRequest, body []byte) {
	t.Helper()
	u, err := url.Parse(up.Url)
	if err != nil {
		t.Fatalf("parse link: %v", err)
	}
	req := httptest.NewRequest(up.Method, u.RequestURI(), bytes.NewReader(body))
	for k, v := range up.Headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	in.e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("put file: %d %s", rec.Code, rec.Body)
	}
}

func TestConversationsAPI_ListFiltersAndCursor(t *testing.T) {
	in := setupInbox(t)
	var ids []uuid.UUID
	for range 3 {
		ids = append(ids, in.conversation(t).Id)
	}
	if rec := in.op.do(http.MethodPut, "/api/v1/conversations/"+ids[0].String()+"/assignee", map[string]any{"assignee_id": in.sop.User.Id}); rec.Code != http.StatusOK {
		t.Fatalf("take: %d", rec.Code)
	}

	var got []uuid.UUID
	path := "/api/v1/conversations?limit=2"
	for path != "" {
		page := decode[oapi.ConversationPage](t, in.op.do(http.MethodGet, path, nil))
		for _, c := range page.Items {
			got = append(got, c.Id)
		}
		path = ""
		if page.NextCursor != nil {
			path = "/api/v1/conversations?limit=2&cursor=" + url.QueryEscape(*page.NextCursor)
		}
	}
	// Newest activity first; taking a conversation is not activity.
	if len(got) != 3 || got[0] != ids[2] || got[1] != ids[1] || got[2] != ids[0] {
		t.Fatalf("all: got %v, want %v reversed", got, ids)
	}

	mine := decode[oapi.ConversationPage](t, in.op.do(http.MethodGet, "/api/v1/conversations?assignee=me", nil))
	none := decode[oapi.ConversationPage](t, in.op.do(http.MethodGet, "/api/v1/conversations?assignee=none&status=queued", nil))
	if len(mine.Items) != 1 || mine.Items[0].Id != ids[0] || len(none.Items) != 2 {
		t.Fatalf("mine %+v, none %+v", mine.Items, none.Items)
	}
	for _, bad := range []string{"?limit=0", "?limit=101", "?assignee=someone", "?status=archived", "?cursor=!!"} {
		wantProblem(t, in.op.do(http.MethodGet, "/api/v1/conversations"+bad, nil), http.StatusBadRequest, "invalid_request")
	}
}

// RBAC through HTTP (docs/00 §11.3): each new permission with a role that
// has it and one that does not.
func TestConversationsAPI_RBAC(t *testing.T) {
	in := setupInbox(t)
	c := in.conversation(t)
	base := "/api/v1/conversations/" + c.Id.String()
	contact := decode[oapi.ContactPage](t, in.op.do(http.MethodGet, "/api/v1/contacts", nil)).Items[0]

	// conversations.create: owner yes, operator no.
	rec := in.op.do(http.MethodPost, "/api/v1/conversations", map[string]any{"contact_id": contact.Id})
	wantProblem(t, rec, http.StatusForbidden, "forbidden")
	// conversations.assign: the operator may not hand a conversation to the owner.
	rec = in.op.do(http.MethodPut, base+"/assignee", map[string]any{"assignee_id": in.so.User.Id})
	wantProblem(t, rec, http.StatusForbidden, "not_assignee")
	rec = in.owner.do(http.MethodPut, base+"/assignee", map[string]any{"assignee_id": in.so.User.Id})
	if rec.Code != http.StatusOK {
		t.Fatalf("owner assigns: %d %s", rec.Code, rec.Body)
	}
	// conversations.manage: the owner replies anywhere, the operator only in own.
	wantProblem(t, in.op.do(http.MethodPost, base+"/status", map[string]any{"status": "resolved"}), http.StatusForbidden, "not_assignee")
	// conversations.note: the operator may note in someone else's conversation.
	if rec := in.op.do(http.MethodPost, base+"/messages", map[string]any{"body": "fyi", "private": true}); rec.Code != http.StatusCreated {
		t.Fatalf("operator note: %d %s", rec.Code, rec.Body)
	}
	// contacts.view / contacts.edit: operators have both.
	name := "Ivan Petrov"
	if rec := in.op.do(http.MethodPatch, "/api/v1/contacts/"+contact.Id.String(), map[string]any{"name": name}); rec.Code != http.StatusOK ||
		decode[oapi.Contact](t, rec).Name != name {
		t.Fatalf("operator edits contact: %d %s", rec.Code, rec.Body)
	}
	// A malformed id is a missing resource.
	wantProblem(t, in.op.do(http.MethodGet, "/api/v1/conversations/not-a-uuid", nil), http.StatusNotFound, "not_found")
}

// Tenant isolation (docs/00 §11.3): every new endpoint answers 404 to
// another workspace and leaks nothing in lists.
func TestIsolation_ConversationsEndpoints(t *testing.T) {
	in := setupInbox(t)
	c := in.conversation(t)
	att := decode[oapi.AttachmentUpload](t, in.owner.do(http.MethodPost, "/api/v1/conversations/"+c.Id.String()+"/attachments",
		map[string]any{"file_name": "a.txt", "content_type": "text/plain", "size": 1}))
	stranger, _ := in.e.register("stranger@example.test", "stranger")

	base := "/api/v1/conversations/" + c.Id.String()
	contact := "/api/v1/contacts/" + c.Contact.Id.String()
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, base, nil},
		{http.MethodPost, base + "/status", map[string]any{"status": "resolved"}},
		{http.MethodPut, base + "/assignee", map[string]any{"assignee_id": nil}},
		{http.MethodGet, base + "/messages", nil},
		{http.MethodPost, base + "/messages", map[string]any{"body": "x", "private": true}},
		{http.MethodPost, base + "/attachments", map[string]any{"file_name": "a.txt", "content_type": "text/plain", "size": 1}},
		{http.MethodGet, "/api/v1/attachments/" + att.Attachment.Id.String() + "/download-link", nil},
		{http.MethodGet, contact, nil},
		{http.MethodPatch, contact, map[string]any{"name": "hijack"}},
	} {
		wantProblem(t, stranger.do(tc.method, tc.path, tc.body), http.StatusNotFound, "not_found")
	}
	// A conversation for someone else's contact is rejected, not created.
	wantProblem(t, stranger.do(http.MethodPost, "/api/v1/conversations", map[string]any{"contact_id": c.Contact.Id}),
		http.StatusBadRequest, "invalid_request")
	if l := decode[oapi.ConversationPage](t, stranger.do(http.MethodGet, "/api/v1/conversations", nil)); len(l.Items) != 0 {
		t.Fatalf("stranger lists conversations: %+v", l.Items)
	}
	if l := decode[oapi.ContactPage](t, stranger.do(http.MethodGet, "/api/v1/contacts", nil)); len(l.Items) != 0 {
		t.Fatalf("stranger lists contacts: %+v", l.Items)
	}
	// The owner still sees everything unchanged.
	if got := decode[oapi.Conversation](t, in.owner.do(http.MethodGet, base, nil)); got.Version != c.Version || got.Contact.Name != "Ivan" {
		t.Fatalf("changed by stranger: %+v", got)
	}
}
