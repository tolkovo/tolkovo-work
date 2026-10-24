package api

import (
	"net/http"
	"testing"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
)

// Owners and admins manage lines; operators read them, escalate their own
// conversations and filter the inbox by line (ADR-0021).
func TestLines(t *testing.T) {
	in := setupInbox(t)
	op := in.sop.User.Id
	body := map[string]any{"name": "Биллинг", "level": 2, "member_ids": []any{op}}
	wantProblem(t, in.op.do(http.MethodPost, "/api/v1/lines", body), http.StatusForbidden, "forbidden")
	wantProblem(t, in.owner.do(http.MethodPost, "/api/v1/lines", map[string]any{"name": "Биллинг", "level": 2}), http.StatusBadRequest, "invalid_request")
	wantProblem(t, in.owner.do(http.MethodPost, "/api/v1/lines", map[string]any{"name": "Биллинг", "level": 4, "member_ids": []any{}}), http.StatusBadRequest, "invalid_request")
	rec := in.owner.do(http.MethodPost, "/api/v1/lines", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	billing := decode[oapi.Line](t, rec)
	wantProblem(t, in.owner.do(http.MethodPost, "/api/v1/lines", map[string]any{"name": "биллинг", "level": 1, "member_ids": []any{}}), http.StatusConflict, "line_exists")
	list := decode[oapi.LineList](t, in.op.do(http.MethodGet, "/api/v1/lines", nil))
	if len(list.Items) != 1 || list.Items[0].Level != 2 || len(list.Items[0].MemberIds) != 1 {
		t.Fatalf("list: %+v", list)
	}

	// The operator takes a conversation and raises it with a reason.
	c := in.conversation(t)
	base := "/api/v1/conversations/" + c.Id.String()
	if rec := in.op.do(http.MethodPut, base+"/assignee", map[string]any{"assignee_id": op, "version": c.Version}); rec.Code != http.StatusOK {
		t.Fatalf("take: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, in.op.do(http.MethodPost, base+"/escalate", map[string]any{"line_id": billing.Id}), http.StatusBadRequest, "invalid_request")
	wantProblem(t, in.op.do(http.MethodPost, base+"/escalate", map[string]any{"line_id": billing.Id, "reason": ""}), http.StatusBadRequest, "invalid_request")
	rec = in.op.do(http.MethodPost, base+"/escalate", map[string]any{"line_id": billing.Id, "reason": "Двойное списание"})
	if rec.Code != http.StatusOK {
		t.Fatalf("escalate: %d %s", rec.Code, rec.Body)
	}
	got := decode[oapi.Conversation](t, rec)
	if got.LineId == nil || *got.LineId != billing.Id || got.LineLevel != 2 || got.Status != oapi.ConversationStatusQueued || got.AssigneeId != nil {
		t.Fatalf("escalated: %+v", got)
	}
	feed := decode[oapi.MessagePage](t, in.op.do(http.MethodGet, base+"/messages", nil))
	var line *oapi.MessageEvent
	for _, m := range feed.Items {
		if m.Event != nil && m.Event.Type == oapi.MessageEventTypeLineChanged {
			line = m.Event
		}
	}
	if line == nil || line.LineId == nil || *line.LineId != billing.Id || line.PreviousLineId != nil {
		t.Fatalf("feed line: %+v", line)
	}
	// Not theirs any more: the operator cannot move it again.
	wantProblem(t, in.op.do(http.MethodPost, base+"/escalate", map[string]any{"line_id": nil, "reason": ""}), http.StatusForbidden, "not_assignee")

	// The inbox filters by line.
	page := decode[oapi.ConversationPage](t, in.op.do(http.MethodGet, "/api/v1/conversations?line="+billing.Id.String(), nil))
	if len(page.Items) != 1 || page.Items[0].Id != c.Id {
		t.Fatalf("line filter: %+v", page.Items)
	}
	if page := decode[oapi.ConversationPage](t, in.op.do(http.MethodGet, "/api/v1/conversations?line=first", nil)); len(page.Items) != 0 {
		t.Fatalf("first line: %+v", page.Items)
	}
	wantProblem(t, in.op.do(http.MethodGet, "/api/v1/conversations?line=x", nil), http.StatusBadRequest, "invalid_request")

	// Deleted: back to the first line.
	wantProblem(t, in.op.do(http.MethodDelete, "/api/v1/lines/"+billing.Id.String(), nil), http.StatusForbidden, "forbidden")
	if rec := in.owner.do(http.MethodDelete, "/api/v1/lines/"+billing.Id.String(), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if got := decode[oapi.Conversation](t, in.op.do(http.MethodGet, base, nil)); got.LineId != nil || got.LineLevel != 1 {
		t.Fatalf("after delete: %+v", got)
	}
}
