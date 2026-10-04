package conversations

import (
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
)

// Status is the state of a conversation (docs/02 §8).
type Status string

// Conversation states.
const (
	StatusNew      Status = "new"      // first message arrived, nobody decided yet
	StatusBot      Status = "bot"      // the AI answers
	StatusQueued   Status = "queued"   // waits for an operator
	StatusAssigned Status = "assigned" // an operator works on it
	StatusPending  Status = "pending"  // the operator waits for the customer
	StatusResolved Status = "resolved" // done; reopens within the reopen window
	StatusClosed   Status = "closed"   // final; a new message starts a new conversation
)

// transitions is the table of allowed status changes from docs/02 §8.
// Transfers (assigned → assigned to someone else) are not status changes
// and go through Assign.
var transitions = map[Status][]Status{
	StatusNew:      {StatusBot, StatusQueued},
	StatusBot:      {StatusQueued, StatusResolved},
	StatusQueued:   {StatusAssigned},
	StatusAssigned: {StatusQueued, StatusPending, StatusResolved},
	StatusPending:  {StatusAssigned, StatusResolved},
	StatusResolved: {StatusAssigned, StatusQueued, StatusClosed},
	StatusClosed:   nil,
}

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	_, ok := transitions[s]
	return ok
}

// CanTransition reports whether the state machine allows from → to.
func CanTransition(from, to Status) bool {
	return slices.Contains(transitions[from], to)
}

// needsAssignee mirrors the CHECK constraints of the conversations table:
// assigned and pending have an assignee, new, bot and queued have none.
func needsAssignee(s Status) bool { return s == StatusAssigned || s == StatusPending }

func forbidsAssignee(s Status) bool {
	return s == StatusNew || s == StatusBot || s == StatusQueued
}

// state is the part of a conversation a transition changes.
type state struct {
	Status     Status
	AssigneeID *uuid.UUID
	ResolvedAt *time.Time
	ClosedAt   *time.Time
}

// transition computes the state after a status change. Going back to the
// queue releases the assignee; resolved and closed stamp their time; a
// move into assigned keeps the assignee it already has (a reopen by the
// same operator) and is refused without one: assigning someone is Assign.
func transition(cur state, to Status, now time.Time) (state, error) {
	if !to.Valid() {
		return state{}, fmt.Errorf("%w: unknown status %q", ErrInvalid, to)
	}
	if !CanTransition(cur.Status, to) {
		return state{}, fmt.Errorf("%w: %s → %s", ErrTransition, cur.Status, to)
	}
	next := cur
	next.Status = to
	switch {
	case forbidsAssignee(to):
		next.AssigneeID = nil
	case needsAssignee(to) && cur.AssigneeID == nil:
		return state{}, fmt.Errorf("%w: %s needs an assignee, assign the conversation instead", ErrTransition, to)
	}
	switch to {
	case StatusResolved:
		next.ResolvedAt = &now
	case StatusClosed:
		next.ClosedAt = &now
	case StatusAssigned, StatusQueued:
		next.ResolvedAt = nil // reopened
	}
	return next, nil
}

// assign computes the state after giving the conversation to assignee, or
// releasing it to the queue when assignee is nil.
//
//   - queued or resolved + someone → assigned (take, reopen);
//   - assigned or pending + someone → same status, new assignee (transfer);
//   - assigned + nobody → queued (release);
//   - new, bot and closed cannot be assigned: the bot or the router decides
//     first, and a closed conversation is final.
func assign(cur state, assignee *uuid.UUID, now time.Time) (state, error) {
	if assignee == nil {
		return transition(cur, StatusQueued, now)
	}
	switch cur.Status {
	case StatusQueued, StatusResolved:
		next := cur
		next.AssigneeID = assignee
		return transition(next, StatusAssigned, now)
	case StatusAssigned, StatusPending:
		next := cur
		next.AssigneeID = assignee
		return next, nil
	default:
		return state{}, fmt.Errorf("%w: a %s conversation cannot be assigned", ErrTransition, cur.Status)
	}
}
