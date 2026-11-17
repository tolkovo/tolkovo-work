package tasks

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/tasks/store"
)

// Moving a task to another project (ADR-0022, P4).

// Move is where a task goes: the project, its queue there (nil: none) and
// its assignee there (nil: none). An assignee who does not work in the
// project is refused, unless AddAssignee asks to add them to it, which only
// a viewer who sees every project (tasks.manage) may.
type Move struct {
	ProjectID   uuid.UUID
	QueueID     *uuid.UUID
	AssigneeID  *uuid.UUID
	AddAssignee bool
	Version     int32
}

// Move puts the task in another project under its next number. The old ref
// stays an alias, so CRM-46 still opens what is APP-19 now; conversations,
// links, comments, files, tags, status, priority and the block stay with
// the task. The viewer must work in the target project or see every
// project.
func (s *Service) Move(ctx context.Context, workspaceID uuid.UUID, v Viewer, taskID uuid.UUID, in Move) (Task, error) {
	var out Task
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		cur, err := q.GetTaskForUpdate(ctx, store.GetTaskForUpdateParams{WorkspaceID: workspaceID, ID: taskID})
		if err != nil {
			return err
		}
		if cur.Version != in.Version {
			return ErrConflict
		}
		if cur.ProjectID == in.ProjectID {
			return fmt.Errorf("%w: the task is in this project already", ErrInvalid)
		}
		from, err := q.GetProject(ctx, store.GetProjectParams{WorkspaceID: workspaceID, ID: cur.ProjectID})
		if err != nil {
			return err
		}
		to, err := s.taskProject(ctx, q, workspaceID, &v, &in.ProjectID)
		if err != nil {
			return err
		}
		if err := checkQueue(ctx, q, workspaceID, to.ID, in.QueueID); err != nil {
			return err
		}
		if err := s.moveAssignee(ctx, q, workspaceID, v, to.ID, in); err != nil {
			return err
		}
		n, err := q.NextProjectNumber(ctx, store.NextProjectNumberParams{WorkspaceID: workspaceID, ID: to.ID})
		if err != nil {
			return err
		}
		if err := q.InsertRefAlias(ctx, store.InsertRefAliasParams{
			WorkspaceID: workspaceID, ProjectID: cur.ProjectID, Number: cur.Number, TaskID: taskID, CreatedAt: s.now(),
		}); err != nil {
			return err
		}
		row, err := q.MoveTask(ctx, store.MoveTaskParams{
			ProjectID: to.ID, Number: n, QueueID: in.QueueID, AssigneeID: in.AssigneeID, UpdatedAt: s.now(), WorkspaceID: workspaceID, ID: taskID,
		})
		if err != nil {
			return err
		}
		actor := v.UserID
		meta := map[string]any{"from": Ref(from.Key, cur.Number), "to": Ref(to.Key, n)}
		if _, err := s.addActivity(ctx, q, row, &actor, "moved", nil, meta); err != nil {
			return err
		}
		// The queue and assignee lines, the new assignee's notice and the
		// event; a task left without an assignee is news to its new queue.
		if err := s.recordChanges(ctx, tx, q, cur, row, actor); err != nil {
			return err
		}
		if row.AssigneeID == nil && !closed(row.Status) {
			if err := s.notifyNew(ctx, tx, q, row, actor); err != nil {
				return err
			}
		}
		if out, err = loadTask(ctx, q, workspaceID, taskID); err != nil {
			return err
		}
		items := []Task{out}
		err = fillWaiting(ctx, q, workspaceID, v, items)
		out = items[0]
		return err
	})
	if err != nil {
		return Task{}, dbError(err, "move task")
	}
	return out, nil
}

// moveAssignee checks the assignee of a moving task against its new
// project, adding them to it when asked and allowed.
func (s *Service) moveAssignee(ctx context.Context, q *store.Queries, workspaceID uuid.UUID, v Viewer, projectID uuid.UUID, in Move) error {
	if in.AssigneeID == nil {
		return nil
	}
	ok, err := q.IsProjectMember(ctx, store.IsProjectMemberParams{WorkspaceID: workspaceID, ProjectID: projectID, UserID: *in.AssigneeID})
	if err != nil || ok {
		return err
	}
	if !in.AddAssignee {
		return fmt.Errorf("%w: the assignee is not a member of the project", ErrMember)
	}
	if !v.All {
		return fmt.Errorf("%w: only who manages tasks adds members to a project", ErrInvalid)
	}
	return s.setProjectMembers(ctx, q, workspaceID, projectID, []uuid.UUID{*in.AssigneeID})
}
