package tasks

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/tasks/store"
)

// Tags of tasks (migration 00037): what a task is about — design,
// interface, backend. A list of the workspace's own, apart from the tags of
// conversations. Any member adds one while tagging a task; renaming and
// deleting are for owners and admins (tasks.manage).

// Limits of tags.
const (
	MaxTags     = 200 // per workspace; the list returns all of them
	MaxTaskTags = 10
	MaxTagName  = 40
)

// Tag labels tasks.
type Tag struct {
	ID        uuid.UUID
	Name      string
	Color     string
	CreatedAt time.Time
}

// TagInput is a tag to create or change; an empty Color is gray.
type TagInput struct {
	Name  string
	Color string
}

// Tags lists the tags of the workspace by name.
func (s *Service) Tags(ctx context.Context, workspaceID uuid.UUID) ([]Tag, error) {
	out := []Tag{}
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		rows, err := store.New(tx).ListTaskTags(ctx, workspaceID)
		for _, r := range rows {
			out = append(out, tagFrom(r))
		}
		return err
	})
	if err != nil {
		return nil, dbError(err, "list tags")
	}
	return out, nil
}

// CreateTag adds a tag by actorID. A name already taken, compared
// case-insensitively, is ErrTagTaken.
func (s *Service) CreateTag(ctx context.Context, workspaceID, actorID uuid.UUID, in TagInput) (Tag, error) {
	name, color, err := validTag(in)
	if err != nil {
		return Tag{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Tag{}, err
	}
	var out Tag
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		n, err := q.CountTaskTags(ctx, workspaceID)
		if err != nil {
			return err
		}
		if n >= MaxTags {
			return fmt.Errorf("%w: at most %d tags", ErrInvalid, MaxTags)
		}
		row, err := q.InsertTaskTag(ctx, store.InsertTaskTagParams{
			ID: id, WorkspaceID: workspaceID, Name: name, Color: color, CreatedBy: &actorID, CreatedAt: s.now(),
		})
		out = tagFrom(row)
		return err
	})
	if err != nil {
		return Tag{}, dbError(err, "create tag")
	}
	return out, nil
}

// UpdateTag renames a tag and changes its color; its tasks keep it.
func (s *Service) UpdateTag(ctx context.Context, workspaceID, tagID uuid.UUID, in TagInput) (Tag, error) {
	name, color, err := validTag(in)
	if err != nil {
		return Tag{}, err
	}
	var out Tag
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		row, err := store.New(tx).UpdateTaskTag(ctx, store.UpdateTaskTagParams{Name: name, Color: color, WorkspaceID: workspaceID, ID: tagID})
		out = tagFrom(row)
		return err
	})
	if err != nil {
		return Tag{}, dbError(err, "update tag")
	}
	return out, nil
}

// DeleteTag removes a tag from the workspace and from its tasks. Their
// feeds keep no line about it: the tag is gone, not changed on a task.
func (s *Service) DeleteTag(ctx context.Context, workspaceID, tagID uuid.UUID) error {
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		n, err := store.New(tx).DeleteTaskTag(ctx, store.DeleteTaskTagParams{WorkspaceID: workspaceID, ID: tagID})
		if err == nil && n == 0 {
			return ErrNotFound
		}
		return err
	})
	if err != nil {
		return dbError(err, "delete tag")
	}
	return nil
}

func validTag(in TagInput) (name, color string, err error) {
	name = strings.Join(strings.Fields(in.Name), " ")
	color = in.Color
	if color == "" {
		color = "gray"
	}
	switch {
	case name == "" || utf8.RuneCountInString(name) > MaxTagName || !validText(name):
		return "", "", fmt.Errorf("%w: tag name must be 1 to %d characters", ErrInvalid, MaxTagName)
	case !slices.Contains(conversations.TagColors, color):
		return "", "", fmt.Errorf("%w: unknown color %q", ErrInvalid, color)
	}
	return name, color, nil
}

// validTagIDs checks a set of tags for a task: unique and at most
// MaxTaskTags. Whether they exist is checked in the transaction.
func validTagIDs(ids []uuid.UUID) error {
	sorted := slices.Clone(ids)
	slices.SortFunc(sorted, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	switch {
	case len(slices.Compact(sorted)) != len(ids):
		return fmt.Errorf("%w: tag ids must be unique", ErrInvalid)
	case len(ids) > MaxTaskTags:
		return fmt.Errorf("%w: at most %d tags per task", ErrInvalid, MaxTaskTags)
	}
	return nil
}

// setTags replaces the tags of a task with ids and returns what it put on
// and took off. A tag of another workspace or a deleted one is ErrInvalid.
func (s *Service) setTags(ctx context.Context, q *store.Queries, row store.Task, ids []uuid.UUID) (added, removed []uuid.UUID, err error) {
	if ids == nil {
		ids = []uuid.UUID{} // NULL would make "NOT (tag_id = ANY(keep))" remove nothing
	}
	n, err := q.CountTaskTagsByID(ctx, store.CountTaskTagsByIDParams{WorkspaceID: row.WorkspaceID, Ids: ids})
	if err != nil {
		return nil, nil, err
	}
	if n != int64(len(ids)) {
		return nil, nil, fmt.Errorf("%w: unknown tag", ErrInvalid)
	}
	removed, err = q.RemoveTaskTags(ctx, store.RemoveTaskTagsParams{WorkspaceID: row.WorkspaceID, TaskID: row.ID, Keep: ids})
	if err != nil {
		return nil, nil, err
	}
	added, err = q.AddTaskTags(ctx, store.AddTaskTagsParams{WorkspaceID: row.WorkspaceID, TaskID: row.ID, CreatedAt: s.now(), TagIds: ids})
	if err != nil {
		return nil, nil, err
	}
	return added, removed, nil
}

// taskTagIDs returns the tags of one task.
func taskTagIDs(ctx context.Context, q *store.Queries, workspaceID, taskID uuid.UUID) ([]uuid.UUID, error) {
	items := []Task{{ID: taskID}}
	if err := fillTags(ctx, q, workspaceID, items); err != nil {
		return nil, err
	}
	return items[0].TagIDs, nil
}

// fillTags sets TagIDs of tasks with one query.
func fillTags(ctx context.Context, q *store.Queries, workspaceID uuid.UUID, items []Task) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(items))
	at := make(map[uuid.UUID]int, len(items))
	for i := range items {
		ids[i] = items[i].ID
		at[items[i].ID] = i
		items[i].TagIDs = []uuid.UUID{}
	}
	rows, err := q.TagsOfTasks(ctx, store.TagsOfTasksParams{WorkspaceID: workspaceID, TaskIds: ids})
	if err != nil {
		return err
	}
	for _, r := range rows {
		if i, ok := at[r.TaskID]; ok {
			items[i].TagIDs = append(items[i].TagIDs, r.TagID)
		}
	}
	return nil
}

func tagFrom(t store.TaskTag) Tag {
	return Tag{ID: t.ID, Name: t.Name, Color: t.Color, CreatedAt: t.CreatedAt}
}
