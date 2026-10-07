package conversations

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

var allStatuses = []Status{
	StatusNew, StatusBot, StatusQueued, StatusAssigned, StatusPending, StatusResolved, StatusClosed,
}

// The whole table of docs/02 §8, both allowed and forbidden pairs, so a
// change to the state machine is a visible change to this test.
func TestCanTransition_MatchesStateMachine(t *testing.T) {
	allowed := map[[2]Status]bool{
		{StatusNew, StatusBot}:           true,
		{StatusNew, StatusQueued}:        true,
		{StatusBot, StatusQueued}:        true,
		{StatusBot, StatusAssigned}:      true,
		{StatusBot, StatusResolved}:      true,
		{StatusQueued, StatusAssigned}:   true,
		{StatusAssigned, StatusQueued}:   true,
		{StatusAssigned, StatusPending}:  true,
		{StatusAssigned, StatusResolved}: true,
		{StatusPending, StatusAssigned}:  true,
		{StatusPending, StatusResolved}:  true,
		{StatusResolved, StatusAssigned}: true,
		{StatusResolved, StatusQueued}:   true,
		{StatusResolved, StatusClosed}:   true,
	}
	for _, from := range allStatuses {
		for _, to := range allStatuses {
			if got, want := CanTransition(from, to), allowed[[2]Status{from, to}]; got != want {
				t.Errorf("CanTransition(%s, %s) = %v, want %v", from, to, got, want)
			}
		}
	}
	if CanTransition("bogus", StatusQueued) || CanTransition(StatusQueued, "bogus") {
		t.Error("unknown statuses must not transition")
	}
}

func TestTransition_SideEffects(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	op := uuid.New()
	earlier := now.Add(-time.Hour)

	tests := []struct {
		name    string
		cur     state
		to      Status
		want    state
		wantErr error
	}{
		{"release clears the assignee", state{Status: StatusAssigned, AssigneeID: &op}, StatusQueued,
			state{Status: StatusQueued}, nil},
		{"pending keeps the assignee", state{Status: StatusAssigned, AssigneeID: &op}, StatusPending,
			state{Status: StatusPending, AssigneeID: &op}, nil},
		{"resolve stamps the time", state{Status: StatusPending, AssigneeID: &op}, StatusResolved,
			state{Status: StatusResolved, AssigneeID: &op, ResolvedAt: &now}, nil},
		{"reopen by the same operator", state{Status: StatusResolved, AssigneeID: &op, ResolvedAt: &earlier}, StatusAssigned,
			state{Status: StatusAssigned, AssigneeID: &op}, nil},
		{"reopen to the queue", state{Status: StatusResolved, AssigneeID: &op, ResolvedAt: &earlier}, StatusQueued,
			state{Status: StatusQueued}, nil},
		{"close stamps the time", state{Status: StatusResolved, AssigneeID: &op, ResolvedAt: &earlier}, StatusClosed,
			state{Status: StatusClosed, AssigneeID: &op, ResolvedAt: &earlier, ClosedAt: &now}, nil},
		{"assigned needs an assignee", state{Status: StatusQueued}, StatusAssigned, state{}, ErrTransition},
		{"reopen without an assignee", state{Status: StatusResolved}, StatusAssigned, state{}, ErrTransition},
		{"closed is final", state{Status: StatusClosed}, StatusQueued, state{}, ErrTransition},
		{"no skipping the queue", state{Status: StatusNew}, StatusAssigned, state{}, ErrTransition},
		{"unknown status", state{Status: StatusQueued}, "archived", state{}, ErrInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := transition(tc.cur, tc.to, now)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if err == nil && !sameState(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestAssign_SideEffects(t *testing.T) {
	now := time.Now().UTC()
	a, b := uuid.New(), uuid.New()
	tests := []struct {
		name     string
		cur      state
		assignee *uuid.UUID
		want     state
		wantErr  error
	}{
		{"take from the queue", state{Status: StatusQueued}, &a, state{Status: StatusAssigned, AssigneeID: &a}, nil},
		{"transfer keeps assigned", state{Status: StatusAssigned, AssigneeID: &a}, &b, state{Status: StatusAssigned, AssigneeID: &b}, nil},
		{"transfer keeps pending", state{Status: StatusPending, AssigneeID: &a}, &b, state{Status: StatusPending, AssigneeID: &b}, nil},
		{"reopen to another operator", state{Status: StatusResolved, AssigneeID: &a, ResolvedAt: &now}, &b,
			state{Status: StatusAssigned, AssigneeID: &b}, nil},
		{"release to the queue", state{Status: StatusAssigned, AssigneeID: &a}, nil, state{Status: StatusQueued}, nil},
		{"pending cannot be released", state{Status: StatusPending, AssigneeID: &a}, nil, state{}, ErrTransition},
		{"taken from the bot", state{Status: StatusBot}, &a, state{Status: StatusAssigned, AssigneeID: &a}, nil},
		{"handed over by hand", state{Status: StatusBot}, nil, state{Status: StatusQueued}, nil},
		{"new is routed first", state{Status: StatusNew}, &a, state{}, ErrTransition},
		{"closed is final", state{Status: StatusClosed, AssigneeID: &a}, &b, state{}, ErrTransition},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := assign(tc.cur, tc.assignee, now)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if err == nil && !sameState(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// Every reachable state satisfies the assignee CHECK constraints of the
// conversations table, so the database never rejects a computed state.
func TestTransitions_KeepAssigneeInvariant(t *testing.T) {
	op := uuid.New()
	now := time.Now().UTC()
	for _, from := range allStatuses {
		cur := state{Status: from}
		if !forbidsAssignee(from) {
			cur.AssigneeID = &op
		}
		for _, to := range allStatuses {
			next, err := transition(cur, to, now)
			if err != nil {
				continue
			}
			if needsAssignee(next.Status) && next.AssigneeID == nil {
				t.Errorf("%s → %s: %s without an assignee", from, to, next.Status)
			}
			if forbidsAssignee(next.Status) && next.AssigneeID != nil {
				t.Errorf("%s → %s: %s with an assignee", from, to, next.Status)
			}
		}
	}
}

func TestCursor_RoundTripAndRejectsGarbage(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	ts := time.Date(2026, 10, 4, 12, 30, 15, 123456000, time.UTC)
	gotAt, gotID, err := decodeTimeCursor(encodeTimeCursor(ts, id))
	if err != nil || !gotAt.Equal(ts) || *gotID != id {
		t.Fatalf("time cursor: %v %v %v", gotAt, gotID, err)
	}
	if got, err := decodeIDCursor(encodeIDCursor(id)); err != nil || *got != id {
		t.Fatalf("id cursor: %v %v", got, err)
	}
	for _, bad := range []string{"!!", "bm90LWEtdXVpZA", encodeIDCursor(id)} {
		if _, _, err := decodeTimeCursor(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("time cursor %q: want ErrInvalid, got %v", bad, err)
		}
	}
	if _, err := decodeIDCursor("bm90LWEtdXVpZA"); !errors.Is(err, ErrInvalid) {
		t.Errorf("id cursor: want ErrInvalid, got %v", err)
	}
}

func sameState(a, b state) bool {
	return a.Status == b.Status && sameID(a.AssigneeID, b.AssigneeID) &&
		sameTime(a.ResolvedAt, b.ResolvedAt) && sameTime(a.ClosedAt, b.ClosedAt)
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}
