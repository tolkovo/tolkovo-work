package tasks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/tasks/store"
)

// Links between tasks (ADR-0022, P3). A task blocks another, which waits
// for it while it is open; two tasks are related; a task repeats another,
// its original. A pair of tasks has one link at most. Links cross
// projects: a task the viewer does not see shows by its status only.

// MaxLinks bounds the links of one task.
const MaxLinks = 50

// The kinds of a link as one of its tasks sees it (Link.Kind).
const (
	// LinkBlocks: this task blocks the other.
	LinkBlocks = "blocks"
	// LinkWaitsFor: the other task blocks this one.
	LinkWaitsFor = "waits_for"
	LinkRelates  = "relates"
	// LinkDuplicates: this task repeats the other, the original.
	LinkDuplicates = "duplicates"
	// LinkDuplicatedBy: the other task repeats this one.
	LinkDuplicatedBy = "duplicated_by"
)

// LinkKinds lists every kind of a link as a task sees it.
var LinkKinds = []string{LinkBlocks, LinkWaitsFor, LinkRelates, LinkDuplicates, LinkDuplicatedBy}

// NoticeUnblocked means the last open task the member's task waited for was
// closed (Notice.Kind).
const NoticeUnblocked = "unblocked"

var (
	// ErrLinkExists means the two tasks are linked already.
	ErrLinkExists = errors.New("tasks: the tasks are linked already")
	// ErrLinkLoop means the tasks would wait for each other.
	ErrLinkLoop = errors.New("tasks: the tasks would wait for each other")
)

// Link is a link of a task to another, as the task sees it.
type Link struct {
	Kind   string
	TaskID uuid.UUID
	// Visible: the viewer sees the other task; otherwise Ref and Title are
	// empty.
	Visible   bool
	Ref       string
	Title     string
	Status    string
	CreatedAt time.Time
}

// Blocker is an open task a task waits for; Ref is "" when the viewer
// does not see it.
type Blocker struct {
	ID  uuid.UUID
	Ref string
}

// stored turns a link as taskID sees it into the stored one.
func stored(taskID, otherID uuid.UUID, kind string) (from, to uuid.UUID, k string) {
	switch kind {
	case LinkBlocks:
		return taskID, otherID, "blocks"
	case LinkWaitsFor:
		return otherID, taskID, "blocks"
	case LinkDuplicates:
		return taskID, otherID, "duplicates"
	case LinkDuplicatedBy:
		return otherID, taskID, "duplicates"
	}
	if bytes.Compare(taskID[:], otherID[:]) < 0 { // the order of uuid in PostgreSQL
		return taskID, otherID, "relates"
	}
	return otherID, taskID, "relates"
}

// seen is a stored link as taskID sees it.
func seen(taskID, from uuid.UUID, kind string) string {
	switch {
	case kind == "blocks" && from == taskID:
		return LinkBlocks
	case kind == "blocks":
		return LinkWaitsFor
	case kind == "duplicates" && from == taskID:
		return LinkDuplicates
	case kind == "duplicates":
		return LinkDuplicatedBy
	}
	return LinkRelates
}

// Links lists the links of a task the viewer sees, oldest first.
func (s *Service) Links(ctx context.Context, workspaceID uuid.UUID, v Viewer, taskID uuid.UUID) ([]Link, error) {
	out := []Link{}
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := visible(ctx, q, workspaceID, v, taskID); err != nil {
			return err
		}
		var err error
		out, err = links(ctx, q, workspaceID, v, taskID)
		return err
	})
	if err != nil {
		return nil, dbError(err, "task links")
	}
	return out, nil
}

func links(ctx context.Context, q *store.Queries, workspaceID uuid.UUID, v Viewer, taskID uuid.UUID) ([]Link, error) {
	rows, err := q.ListTaskLinks(ctx, store.ListTaskLinksParams{Viewer: v.UserID, SeeAll: v.All, TaskID: taskID, WorkspaceID: workspaceID})
	if err != nil {
		return nil, err
	}
	out := make([]Link, 0, len(rows))
	for _, r := range rows {
		l := Link{Kind: seen(taskID, r.FromTaskID, r.Kind), TaskID: r.OtherID, Visible: r.Visible, Status: r.OtherStatus, CreatedAt: r.CreatedAt}
		if r.Visible {
			l.Ref, l.Title = Ref(r.OtherProjectKey, r.OtherNumber), r.OtherTitle
		}
		out = append(out, l)
	}
	return out, nil
}

// AddLink links a task to another by the viewer, kind as the task sees it.
// The viewer must see both tasks.
func (s *Service) AddLink(ctx context.Context, workspaceID uuid.UUID, v Viewer, taskID, otherID uuid.UUID, kind string) ([]Link, error) {
	if !slices.Contains(LinkKinds, kind) {
		return nil, fmt.Errorf("%w: unknown link kind", ErrInvalid)
	}
	if taskID == otherID {
		return nil, fmt.Errorf("%w: a task is not linked to itself", ErrInvalid)
	}
	var out []Link
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		task, other, err := lockPair(ctx, q, workspaceID, v, taskID, otherID)
		if err != nil {
			return err
		}
		if _, err := q.TaskLinkBetween(ctx, store.TaskLinkBetweenParams{WorkspaceID: workspaceID, A: taskID, B: otherID}); err == nil {
			return ErrLinkExists
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := s.insertLink(ctx, tx, q, task, other, kind, v.UserID); err != nil {
			return err
		}
		out, err = links(ctx, q, workspaceID, v, taskID)
		return err
	})
	if err != nil {
		return nil, dbError(err, "link tasks")
	}
	return out, nil
}

// insertLink stores a new link of task to other, kind as task sees it,
// and writes it in both feeds.
func (s *Service) insertLink(ctx context.Context, tx pgx.Tx, q *store.Queries, task, other store.Task, kind string, actorID uuid.UUID) error {
	for _, id := range []uuid.UUID{task.ID, other.ID} {
		n, err := q.CountTaskLinks(ctx, store.CountTaskLinksParams{WorkspaceID: task.WorkspaceID, TaskID: id})
		if err != nil {
			return err
		}
		if n >= MaxLinks {
			return fmt.Errorf("%w: a task has at most %d links", ErrInvalid, MaxLinks)
		}
	}
	from, to, k := stored(task.ID, other.ID, kind)
	if k == "blocks" {
		loop, err := q.BlocksPath(ctx, store.BlocksPathParams{WorkspaceID: task.WorkspaceID, FromTaskID: from, ToTaskID: to})
		if err != nil {
			return err
		}
		if loop {
			return ErrLinkLoop
		}
	}
	n, err := q.InsertTaskLink(ctx, store.InsertTaskLinkParams{
		WorkspaceID: task.WorkspaceID, FromTaskID: from, ToTaskID: to, Kind: k, CreatedBy: &actorID, CreatedAt: s.now(),
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrLinkExists
	}
	return s.linkLines(ctx, tx, q, "link_added", from, k, actorID, task, other)
}

// RemoveLink removes the link of a task the viewer sees to another, which
// they may not see.
func (s *Service) RemoveLink(ctx context.Context, workspaceID uuid.UUID, v Viewer, taskID, otherID uuid.UUID) error {
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := visible(ctx, q, workspaceID, v, taskID); err != nil {
			return err
		}
		task, other, err := lockPair(ctx, q, workspaceID, Viewer{All: true}, taskID, otherID)
		if err != nil {
			return err
		}
		l, err := q.DeleteTaskLink(ctx, store.DeleteTaskLinkParams{WorkspaceID: workspaceID, A: taskID, B: otherID})
		if err != nil {
			return err
		}
		return s.linkLines(ctx, tx, q, "link_removed", l.FromTaskID, l.Kind, v.UserID, task, other)
	})
	if err != nil {
		return dbError(err, "unlink tasks")
	}
	return nil
}

// linkLines writes a link_added or link_removed line in the feeds of both
// tasks, with the kind each sees and the other's ref, and tells clients.
func (s *Service) linkLines(ctx context.Context, tx pgx.Tx, q *store.Queries, kind string, from uuid.UUID, k string, actorID uuid.UUID, a, b store.Task) error {
	refA, err := taskRef(ctx, q, a)
	if err != nil {
		return err
	}
	refB, err := taskRef(ctx, q, b)
	if err != nil {
		return err
	}
	for _, p := range []struct {
		row   store.Task
		other uuid.UUID
		ref   string
	}{{a, b.ID, refB}, {b, a.ID, refA}} {
		meta := map[string]any{"link": seen(p.row.ID, from, k), "task_id": p.other, "ref": p.ref}
		if _, err := s.addActivity(ctx, q, p.row, &actorID, kind, nil, meta); err != nil {
			return err
		}
		if err := changed(ctx, tx, p.row, &actorID, false); err != nil {
			return err
		}
	}
	return nil
}

// lockPair locks two tasks the viewer sees, in the order of their ids so
// two links of the same pair never deadlock, and returns them in the
// order asked.
func lockPair(ctx context.Context, q *store.Queries, workspaceID uuid.UUID, v Viewer, a, b uuid.UUID) (store.Task, store.Task, error) {
	first, second := a, b
	if bytes.Compare(a[:], b[:]) > 0 {
		first, second = b, a
	}
	rows := map[uuid.UUID]store.Task{}
	for _, id := range []uuid.UUID{first, second} {
		if err := visible(ctx, q, workspaceID, v, id); err != nil {
			return store.Task{}, store.Task{}, err
		}
		row, err := q.GetTaskForUpdate(ctx, store.GetTaskForUpdateParams{WorkspaceID: workspaceID, ID: id})
		if err != nil {
			return store.Task{}, store.Task{}, err
		}
		rows[id] = row
	}
	return rows[a], rows[b], nil
}

// visible is ErrNotFound for a task the viewer does not see, or none.
func visible(ctx context.Context, q *store.Queries, workspaceID uuid.UUID, v Viewer, taskID uuid.UUID) error {
	ok, err := q.TaskVisible(ctx, store.TaskVisibleParams{Viewer: v.UserID, SeeAll: v.All, WorkspaceID: workspaceID, ID: taskID})
	if err == nil && !ok {
		return ErrNotFound
	}
	return err
}

// recordBlocks follows a task closed or reopened in the tasks it blocks:
// their plates change, and the assignee of one that waits for nothing now
// is told.
func (s *Service) recordBlocks(ctx context.Context, tx pgx.Tx, q *store.Queries, row store.Task, actorID *uuid.UUID) error {
	rows, err := q.BlockedBy(ctx, store.BlockedByParams{WorkspaceID: row.WorkspaceID, TaskID: row.ID})
	if err != nil {
		return err
	}
	for _, r := range rows {
		t := r.Task
		freed := closed(row.Status) && !r.Waiting
		if err := changedWith(ctx, tx, t, actorID, false, freed); err != nil {
			return err
		}
		if freed && t.AssigneeID != nil && !sameID(t.AssigneeID, actorID) {
			if err := s.notify(ctx, tx, t, NoticeUnblocked, []uuid.UUID{*t.AssigneeID}); err != nil {
				return err
			}
		}
	}
	return nil
}

// FillWaiting writes what a task waits for: the open tasks that block it,
// as the viewer sees them.
func (s *Service) FillWaiting(ctx context.Context, workspaceID uuid.UUID, v Viewer, t *Task) error {
	items := []Task{*t}
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		return fillWaiting(ctx, store.New(tx), workspaceID, v, items)
	})
	*t = items[0]
	if err != nil {
		return dbError(err, "waiting tasks")
	}
	return nil
}

func fillWaiting(ctx context.Context, q *store.Queries, workspaceID uuid.UUID, v Viewer, items []Task) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(items))
	at := make(map[uuid.UUID]int, len(items))
	for i := range items {
		ids[i] = items[i].ID
		at[items[i].ID] = i
		items[i].WaitingOn = []Blocker{}
	}
	rows, err := q.WaitingOn(ctx, store.WaitingOnParams{Viewer: v.UserID, SeeAll: v.All, WorkspaceID: workspaceID, TaskIds: ids})
	if err != nil {
		return err
	}
	for _, r := range rows {
		i, ok := at[r.TaskID]
		if !ok {
			continue
		}
		b := Blocker{ID: r.BlockerID}
		if r.Visible {
			b.Ref = Ref(r.ProjectKey, r.Number)
		}
		items[i].WaitingOn = append(items[i].WaitingOn, b)
	}
	return nil
}

// CloseAsDuplicate cancels a task the viewer sees, read at version, as a
// repeat of the original: the link says so, and its conversations move to
// the original, which they wait for while it is open.
func (s *Service) CloseAsDuplicate(ctx context.Context, workspaceID uuid.UUID, v Viewer, taskID, originalID uuid.UUID, version int32) (Task, error) {
	if taskID == originalID {
		return Task{}, fmt.Errorf("%w: a task is not a duplicate of itself", ErrInvalid)
	}
	actorID := v.UserID
	var out Task
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		cur, orig, err := lockPair(ctx, q, workspaceID, v, taskID, originalID)
		if err != nil {
			return err
		}
		if cur.Version != version {
			return ErrConflict
		}
		if closed(cur.Status) {
			return fmt.Errorf("%w: the task is closed", ErrInvalid)
		}
		// Another link of the two gives way to this one.
		old, err := q.TaskLinkBetween(ctx, store.TaskLinkBetweenParams{WorkspaceID: workspaceID, A: taskID, B: originalID})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			err = s.insertLink(ctx, tx, q, cur, orig, LinkDuplicates, actorID)
		case err != nil:
		case old.Kind == "duplicates" && old.FromTaskID == taskID: // linked so already
		case old.Kind == "duplicates":
			err = fmt.Errorf("%w: the other task is a duplicate of this one", ErrInvalid)
		default:
			if _, err = q.DeleteTaskLink(ctx, store.DeleteTaskLinkParams{WorkspaceID: workspaceID, A: taskID, B: originalID}); err != nil {
				return err
			}
			if err = s.linkLines(ctx, tx, q, "link_removed", old.FromTaskID, old.Kind, actorID, cur, orig); err != nil {
				return err
			}
			err = s.insertLink(ctx, tx, q, cur, orig, LinkDuplicates, actorID)
		}
		if err != nil {
			return err
		}
		if err := s.moveConversations(ctx, tx, q, cur, orig, actorID); err != nil {
			return err
		}
		now := s.now()
		row, err := q.UpdateTask(ctx, store.UpdateTaskParams{
			QueueID: cur.QueueID, TopicID: cur.TopicID, Title: cur.Title, Body: cur.Body, Status: StatusCanceled, Priority: cur.Priority,
			AssigneeID: cur.AssigneeID, ClosedAt: &now, UpdatedAt: now, BlockedReason: nil, ReviewFrom: nil,
			WorkspaceID: workspaceID, ID: taskID,
		})
		if err != nil {
			return err
		}
		if err := s.recordChanges(ctx, tx, q, cur, row, actorID); err != nil {
			return err
		}
		out, err = loadTask(ctx, q, workspaceID, taskID)
		return err
	})
	if err != nil {
		return Task{}, dbError(err, "close as duplicate")
	}
	return out, nil
}

// moveConversations links the conversations of a duplicate to its
// original and unlinks them from the duplicate, before it is closed: they
// go on waiting, for the original.
func (s *Service) moveConversations(ctx context.Context, tx pgx.Tx, q *store.Queries, dup, orig store.Task, actorID uuid.UUID) error {
	convs, err := q.TaskConversationIDs(ctx, store.TaskConversationIDsParams{WorkspaceID: dup.WorkspaceID, TaskID: dup.ID})
	if err != nil || len(convs) == 0 {
		return err
	}
	ref, err := taskRef(ctx, q, orig)
	if err != nil {
		return err
	}
	for _, c := range convs {
		if err := s.link(ctx, tx, q, orig, ref, c, &actorID, conversations.EventTaskLinked); err != nil && !errors.Is(err, ErrLinked) {
			return err
		}
		if err := s.unlink(ctx, tx, q, dup, c, actorID); err != nil {
			return err
		}
	}
	return changed(ctx, tx, orig, &actorID, false)
}
