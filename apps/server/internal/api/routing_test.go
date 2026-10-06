package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
)

func presenceOf(t *testing.T, c *client, user uuid.UUID) oapi.Presence {
	t.Helper()
	for _, m := range decode[oapi.MemberList](t, c.do(http.MethodGet, "/api/v1/members", nil)).Items {
		if m.UserId == user {
			return m.Presence
		}
	}
	t.Fatalf("member %v not listed", user)
	return ""
}

// Owners and admins choose the mode; everyone sees it.
func TestRouting_Settings(t *testing.T) {
	in := setupInbox(t)
	if got := decode[oapi.RoutingSettings](t, in.op.do(http.MethodGet, "/api/v1/routing", nil)); got.Mode != oapi.Manual {
		t.Fatalf("default mode %q", got.Mode)
	}
	wantProblem(t, in.op.do(http.MethodPut, "/api/v1/routing", map[string]any{"mode": "round_robin"}), http.StatusForbidden, "forbidden")
	wantProblem(t, in.owner.do(http.MethodPut, "/api/v1/routing", map[string]any{"mode": "random"}), http.StatusBadRequest, "invalid_request")
	wantProblem(t, in.owner.do(http.MethodPut, "/api/v1/routing", map[string]any{"mode": "manual", "team": "x"}), http.StatusBadRequest, "invalid_request")
	rec := in.owner.do(http.MethodPut, "/api/v1/routing", map[string]any{"mode": "round_robin"})
	if rec.Code != http.StatusOK {
		t.Fatalf("set mode: %d %s", rec.Code, rec.Body)
	}
	if got := decode[oapi.RoutingSettings](t, in.op.do(http.MethodGet, "/api/v1/routing", nil)); got.Mode != oapi.RoundRobin {
		t.Fatalf("mode %q", got.Mode)
	}
}

// A member chooses to accept conversations or not; colleagues see it
// while the panel is open.
func TestRouting_Presence(t *testing.T) {
	in := setupInbox(t)
	ws, op := in.so.Workspace.Id, in.sop.User.Id
	if got := decode[oapi.MyPresence](t, in.op.do(http.MethodGet, "/api/v1/me/presence", nil)); got.Status != oapi.MyPresenceStatusOnline {
		t.Fatalf("default status %q", got.Status)
	}
	if p := presenceOf(t, in.owner, op); p != oapi.PresenceOffline {
		t.Fatalf("no panel open, presence %q", p)
	}
	if err := in.e.routing.Seen(context.Background(), ws, []uuid.UUID{op}); err != nil {
		t.Fatalf("seen: %v", err)
	}
	if p := presenceOf(t, in.owner, op); p != oapi.PresenceOnline {
		t.Fatalf("presence %q", p)
	}
	rec := in.op.do(http.MethodPut, "/api/v1/me/presence", map[string]any{"status": "away"})
	if rec.Code != http.StatusOK || decode[oapi.MyPresence](t, rec).Status != oapi.MyPresenceStatusAway {
		t.Fatalf("set away: %d %s", rec.Code, rec.Body)
	}
	if p := presenceOf(t, in.owner, op); p != oapi.PresenceAway {
		t.Fatalf("presence %q", p)
	}
	wantProblem(t, in.op.do(http.MethodPut, "/api/v1/me/presence", map[string]any{"status": "offline"}), http.StatusBadRequest, "invalid_request")
	wantProblem(t, in.e.client().do(http.MethodGet, "/api/v1/me/presence", nil), http.StatusUnauthorized, "unauthenticated")

	// The status is per workspace: the operator's own one is untouched.
	home := in.e.client()
	home.cookie, home.csrf = in.op.cookie, in.op.csrf
	if rec := home.do(http.MethodPost, "/api/v1/auth/workspace", map[string]any{"workspace_id": in.sop.Workspace.Id}); rec.Code != http.StatusOK {
		t.Fatalf("switch: %d %s", rec.Code, rec.Body)
	}
	if got := decode[oapi.MyPresence](t, home.do(http.MethodGet, "/api/v1/me/presence", nil)); got.Status != oapi.MyPresenceStatusOnline {
		t.Fatalf("other workspace status %q", got.Status)
	}
}

// With round_robin a new conversation goes to the operator with the panel
// open, through the router's job.
func TestRouting_AssignsNewConversations(t *testing.T) {
	in := setupInbox(t)
	ws, op := in.so.Workspace.Id, in.sop.User.Id
	if rec := in.owner.do(http.MethodPut, "/api/v1/routing", map[string]any{"mode": "round_robin"}); rec.Code != http.StatusOK {
		t.Fatalf("set mode: %d %s", rec.Code, rec.Body)
	}
	if rec := in.owner.do(http.MethodPut, "/api/v1/me/presence", map[string]any{"status": "away"}); rec.Code != http.StatusOK {
		t.Fatalf("owner away: %d %s", rec.Code, rec.Body)
	}
	if err := in.e.routing.Seen(context.Background(), ws, []uuid.UUID{op, in.so.User.Id}); err != nil {
		t.Fatalf("seen: %v", err)
	}
	c := in.conversation(t)
	if n := in.e.ownerCount("SELECT count(*) FROM river.river_job WHERE kind = 'routing.dispatch' AND args->>'workspace_id' = $1", ws.String()); n == 0 {
		t.Fatal("no dispatch queued")
	}
	if _, err := in.e.routing.Dispatch(context.Background(), in.e.convs, ws); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	got := decode[oapi.Conversation](t, in.op.do(http.MethodGet, "/api/v1/conversations/"+c.Id.String(), nil))
	if got.Status != oapi.ConversationStatusAssigned || got.AssigneeId == nil || *got.AssigneeId != op {
		t.Fatalf("conversation: %s %v", got.Status, got.AssigneeId)
	}
}
