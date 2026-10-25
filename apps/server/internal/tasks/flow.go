package tasks

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/tasks/store"
)

// Metrics of the flow (ADR-0019, K3): read from the history of statuses,
// without tables of their own.

// Limits of the flow metrics.
const (
	DefaultFlowDays = 30
	MinFlowDays     = 7
	MaxFlowDays     = 90
	// flowTaskLimit bounds the tasks the metrics read, the latest first.
	flowTaskLimit = 5000
	// flowAgingLimit is how many of the oldest open tasks the metrics name.
	flowAgingLimit = 20
)

// FlowFilter narrows the metrics like BoardFilter narrows the board.
type FlowFilter struct {
	AssigneeID *uuid.UUID
	Unassigned bool
	QueueID    *uuid.UUID
	NoQueue    bool
	TopicID    *uuid.UUID
	NoTopic    bool
	TagID      *uuid.UUID
	// Days back the period reaches, today included; 0 is DefaultFlowDays.
	Days int
}

// Flow are the metrics of a period. Days are midnights in the workspace's
// time zone.
type Flow struct {
	Since, Until time.Time
	// Cycle: tasks done in the period that were in progress, in the order
	// they were done.
	Cycle              []FlowCycle
	CycleP50, CycleP85 *float64
	Throughput         []FlowWeek
	CFD                []FlowDay
	Aging              []FlowAging
	// Truncated: there were more tasks than flowTaskLimit.
	Truncated bool
}

// FlowCycle is the cycle time of a task done: from its first move to
// in_progress to done.
type FlowCycle struct {
	TaskID uuid.UUID
	Number int64
	Title  string
	DoneAt time.Time
	Days   float64
}

// FlowWeek counts the tasks done in the week from Monday.
type FlowWeek struct {
	Week time.Time
	Done int
}

// FlowDay counts the tasks in each status at the end of a day; Done only
// those done since the period began.
type FlowDay struct {
	Day                                      time.Time
	Open, InProgress, Review, Verified, Done int
}

// FlowAging is an open task and how long it stands in its status.
type FlowAging struct {
	TaskID  uuid.UUID
	Number  int64
	Title   string
	Status  string
	Days    float64
	Blocked bool
}

// Flow returns the metrics of the last f.Days days.
func (s *Service) Flow(ctx context.Context, workspaceID uuid.UUID, f FlowFilter) (Flow, error) {
	if f.Days == 0 {
		f.Days = DefaultFlowDays
	}
	if f.Days < MinFlowDays || f.Days > MaxFlowDays {
		return Flow{}, fmt.Errorf("%w: days must be %d to %d", ErrInvalid, MinFlowDays, MaxFlowDays)
	}
	var out Flow
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		tz, err := q.WorkspaceTimezone(ctx, workspaceID)
		if err != nil {
			return err
		}
		loc, err := time.LoadLocation(tz)
		if err != nil {
			loc = time.UTC
		}
		now := s.now().In(loc)
		since := midnight(now).AddDate(0, 0, -(f.Days - 1))
		rows, err := q.FlowTasks(ctx, store.FlowTasksParams{
			WorkspaceID: workspaceID, Since: since, AssigneeID: f.AssigneeID, Unassigned: f.Unassigned,
			QueueID: f.QueueID, NoQueue: f.NoQueue, TopicID: f.TopicID, NoTopic: f.NoTopic, TagID: f.TagID, Lim: flowTaskLimit + 1,
		})
		if err != nil {
			return err
		}
		truncated := len(rows) > flowTaskLimit
		rows = rows[:min(len(rows), flowTaskLimit)]
		ids := make([]uuid.UUID, len(rows))
		for i, r := range rows {
			ids[i] = r.ID
		}
		changes, err := q.FlowStatusChanges(ctx, store.FlowStatusChangesParams{WorkspaceID: workspaceID, TaskIds: ids})
		if err != nil {
			return err
		}
		history := make(map[uuid.UUID][]statusChange, len(rows))
		for _, c := range changes {
			history[c.TaskID] = append(history[c.TaskID], statusChange{at: c.CreatedAt, from: c.FromStatus, to: c.ToStatus})
		}
		out = computeFlow(rows, history, since, now)
		out.Truncated = truncated
		return nil
	})
	if err != nil {
		return Flow{}, fmt.Errorf("task flow: %w", err)
	}
	return out, nil
}

type statusChange struct {
	at       time.Time
	from, to string
}

func midnight(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// statusAt is the status of a task at moment at and when it came into it;
// "" before the task existed.
func statusAt(t store.FlowTasksRow, changes []statusChange, at time.Time) (string, time.Time) {
	if t.CreatedAt.After(at) {
		return "", time.Time{}
	}
	status, since := t.Status, t.CreatedAt
	if len(changes) > 0 {
		status = changes[0].from
	}
	for _, c := range changes {
		if c.at.After(at) {
			break
		}
		status, since = c.to, c.at
	}
	return status, since
}

// computeFlow builds the metrics of the days from since to now's day.
func computeFlow(tasks []store.FlowTasksRow, history map[uuid.UUID][]statusChange, since, now time.Time) Flow {
	today := midnight(now)
	out := Flow{Since: since, Until: today, Cycle: []FlowCycle{}, Throughput: []FlowWeek{}, CFD: []FlowDay{}, Aging: []FlowAging{}}

	for d := since; !d.After(today); d = d.AddDate(0, 0, 1) {
		end := d.AddDate(0, 0, 1).Add(-time.Nanosecond)
		if end.After(now) {
			end = now
		}
		day := FlowDay{Day: d}
		for _, t := range tasks {
			status, at := statusAt(t, history[t.ID], end)
			switch status {
			case StatusOpen:
				day.Open++
			case StatusInProgress:
				day.InProgress++
			case StatusReview:
				day.Review++
			case StatusVerified:
				day.Verified++
			case StatusDone:
				if !at.Before(since) {
					day.Done++
				}
			}
		}
		out.CFD = append(out.CFD, day)
	}

	monday := func(t time.Time) time.Time {
		d := midnight(t)
		return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
	}
	weeks := map[time.Time]int{}
	var days []float64
	for _, t := range tasks {
		changes := history[t.ID]
		if t.Status == StatusDone {
			doneAt := t.StatusChangedAt
			var start *time.Time
			for _, c := range changes {
				if c.to == StatusInProgress && start == nil {
					at := c.at
					start = &at
				}
				if c.to == StatusDone {
					doneAt = c.at
				}
			}
			if doneAt.Before(since) {
				continue
			}
			weeks[monday(doneAt.In(since.Location()))]++
			if start != nil && !start.After(doneAt) {
				c := FlowCycle{TaskID: t.ID, Number: t.Number, Title: t.Title, DoneAt: doneAt, Days: roundDays(doneAt.Sub(*start))}
				out.Cycle = append(out.Cycle, c)
				days = append(days, c.Days)
			}
			continue
		}
		if t.ClosedAt == nil {
			out.Aging = append(out.Aging, FlowAging{
				TaskID: t.ID, Number: t.Number, Title: t.Title, Status: t.Status, Days: roundDays(now.Sub(t.StatusChangedAt)), Blocked: t.Blocked,
			})
		}
	}
	slices.SortFunc(out.Cycle, func(a, b FlowCycle) int { return a.DoneAt.Compare(b.DoneAt) })
	slices.Sort(days)
	out.CycleP50, out.CycleP85 = percentile(days, 0.5), percentile(days, 0.85)
	for w := monday(since); !w.After(today); w = w.AddDate(0, 0, 7) {
		out.Throughput = append(out.Throughput, FlowWeek{Week: w, Done: weeks[w]})
	}
	slices.SortStableFunc(out.Aging, func(a, b FlowAging) int { return cmp.Compare(b.Days, a.Days) })
	out.Aging = out.Aging[:min(len(out.Aging), flowAgingLimit)]
	return out
}

func roundDays(d time.Duration) float64 {
	return math.Round(d.Hours()/24*10) / 10
}

// percentile of sorted values by the nearest rank; nil without values.
func percentile(sorted []float64, p float64) *float64 {
	if len(sorted) == 0 {
		return nil
	}
	v := sorted[max(0, int(math.Ceil(p*float64(len(sorted))))-1)]
	return &v
}
