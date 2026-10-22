package tasks

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/tasks/store"
)

// Limits of the board (ADR-0019).
const (
	// BoardColumnLimit is how many tasks a column of the board shows at
	// first; the rest come by List with Flow.
	BoardColumnLimit = 50
	// DefaultClosedDays and MaxClosedDays bound how far back the done
	// column of the board reaches.
	DefaultClosedDays = 14
	MaxClosedDays     = 90
)

// FlowKey is the place of a task in its column of the board, to continue a
// page after it.
type FlowKey struct {
	Rank    int32
	Changed time.Time
	Number  int64
}

// priorityRank orders priorities on the board, most urgent first; it is
// the CASE of BoardTasks and ListTasksFlow.
func priorityRank(p string) int32 {
	switch p {
	case "urgent":
		return 0
	case "high":
		return 1
	case "normal":
		return 2
	}
	return 3
}

func flowKey(t Task) FlowKey {
	return FlowKey{Rank: priorityRank(t.Priority), Changed: t.StatusChangedAt, Number: t.Number}
}

// BoardFilter narrows the board like Filter narrows the list.
type BoardFilter struct {
	AssigneeID *uuid.UUID
	Unassigned bool
	QueueID    *uuid.UUID
	NoQueue    bool
	TopicID    *uuid.UUID
	NoTopic    bool
	TagID      *uuid.UUID
	Query      string
	// ClosedDays is how many days back the done column reaches; 0 is
	// DefaultClosedDays.
	ClosedDays int
	// Canceled adds the column of canceled tasks, as far back as done.
	Canceled bool
	// Blocked, when set, keeps the blocked tasks or the others.
	Blocked *bool
}

// Column is one status of the board: its first tasks in the order of the
// column and how many there are; Next continues it by List with Flow, nil
// when every task is shown.
type Column struct {
	Status string
	Count  int64
	Items  []Task
	Next   *FlowKey
	// WIPLimit and AgeDays are the column's rule (ruleFor): soft, the
	// board only shows them; nil when none.
	WIPLimit *int
	AgeDays  *int
}

// Board returns the board in one query, so the columns agree with each
// other: every status in the order of Statuses, canceled only when asked.
// An open column puts the most urgent task first, then the one standing
// longest in it; done holds the tasks closed in the last ClosedDays days,
// the latest first.
func (s *Service) Board(ctx context.Context, workspaceID uuid.UUID, f BoardFilter) ([]Column, error) {
	if f.ClosedDays == 0 {
		f.ClosedDays = DefaultClosedDays
	}
	if f.ClosedDays < 1 || f.ClosedDays > MaxClosedDays {
		return nil, fmt.Errorf("%w: closed days must be 1 to %d", ErrInvalid, MaxClosedDays)
	}
	if utf8.RuneCountInString(f.Query) > 100 || !validText(f.Query) {
		return nil, fmt.Errorf("%w: query must be at most 100 characters", ErrInvalid)
	}
	cols := make([]Column, 0, len(Statuses))
	at := make(map[string]int, len(Statuses))
	for _, st := range Statuses {
		if st == StatusCanceled && !f.Canceled {
			continue
		}
		at[st] = len(cols)
		cols = append(cols, Column{Status: st, Items: []Task{}})
	}
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		rules, err := columnRules(ctx, q, workspaceID)
		if err != nil {
			return err
		}
		for i := range cols {
			r := ruleFor(rules, f.QueueID, cols[i].Status)
			cols[i].WIPLimit, cols[i].AgeDays = r.WIPLimit, r.AgeDays
		}
		rows, err := q.BoardTasks(ctx, store.BoardTasksParams{
			WorkspaceID: workspaceID, PerColumn: BoardColumnLimit, ClosedSince: s.now().AddDate(0, 0, -f.ClosedDays), Canceled: f.Canceled,
			AssigneeID: f.AssigneeID, Unassigned: f.Unassigned, QueueID: f.QueueID, NoQueue: f.NoQueue, TopicID: f.TopicID, NoTopic: f.NoTopic,
			TagID: f.TagID, Blocked: boolOf(f.Blocked), Query: likeEscape(strings.TrimSpace(f.Query)),
		})
		if err != nil {
			return err
		}
		all := make([]Task, 0, len(rows))
		for _, r := range rows {
			all = append(all, taskFrom(r.Task))
		}
		if err := fillLists(ctx, q, workspaceID, all); err != nil {
			return err
		}
		for i, r := range rows {
			c := &cols[at[r.Task.Status]]
			c.Count = r.ColumnCount
			c.Items = append(c.Items, all[i])
		}
		return nil
	})
	if err != nil {
		return nil, dbError(err, "board")
	}
	for i := range cols {
		if c := &cols[i]; int64(len(c.Items)) < c.Count {
			k := flowKey(c.Items[len(c.Items)-1])
			c.Next = &k
		}
	}
	return cols, nil
}

// listFlow is List with Flow: one status in the order of its column.
func (s *Service) listFlow(ctx context.Context, workspaceID uuid.UUID, f Filter, limit int) (Page, error) {
	if f.Status == "" || f.State != "" {
		return Page{}, fmt.Errorf("%w: the order of the board takes one status and no state", ErrInvalid)
	}
	if f.ClosedDays < 0 || f.ClosedDays > MaxClosedDays {
		return Page{}, fmt.Errorf("%w: closed days must be 1 to %d", ErrInvalid, MaxClosedDays)
	}
	var since *time.Time
	if f.ClosedDays > 0 {
		t := s.now().AddDate(0, 0, -f.ClosedDays)
		since = &t
	}
	p := store.ListTasksFlowParams{
		WorkspaceID: workspaceID, Status: f.Status, ClosedSince: since, ClosedOrder: closed(f.Status),
		AssigneeID: f.AssigneeID, Unassigned: f.Unassigned, QueueID: f.QueueID, NoQueue: f.NoQueue, TopicID: f.TopicID, NoTopic: f.NoTopic,
		TagID: f.TagID, Blocked: boolOf(f.Blocked), Query: likeEscape(strings.TrimSpace(f.Query)), Lim: int32(limit + 1), //nolint:gosec // G115: bounded by pageLimit
	}
	if f.After != nil {
		p.AfterRank, p.AfterChanged, p.AfterNumber = f.After.Rank, f.After.Changed, f.After.Number
	}
	out := Page{Items: []Task{}}
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		rows, err := q.ListTasksFlow(ctx, p)
		if err != nil {
			return err
		}
		for i, r := range rows {
			if i == limit {
				break
			}
			out.Items = append(out.Items, taskFrom(r))
		}
		if len(rows) > limit {
			k := flowKey(out.Items[limit-1])
			out.NextKey = &k
		}
		return fillLists(ctx, q, workspaceID, out.Items)
	})
	if err != nil {
		return Page{}, dbError(err, "list tasks")
	}
	return out, nil
}

// fillLists fills what lists show of a page of tasks: tags and the number
// of linked conversations, one query each.
func fillLists(ctx context.Context, q *store.Queries, workspaceID uuid.UUID, items []Task) error {
	if err := fillTags(ctx, q, workspaceID, items); err != nil || len(items) == 0 {
		return err
	}
	ids := make([]uuid.UUID, len(items))
	at := make(map[uuid.UUID]int, len(items))
	for i := range items {
		ids[i] = items[i].ID
		at[items[i].ID] = i
	}
	rows, err := q.ConversationCounts(ctx, store.ConversationCountsParams{WorkspaceID: workspaceID, TaskIds: ids})
	if err != nil {
		return err
	}
	for _, r := range rows {
		if i, ok := at[r.TaskID]; ok {
			items[i].ConversationCount = int(r.N)
		}
	}
	return nil
}
