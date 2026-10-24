package tasks

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/tasks/store"
)

// The flow metrics from the history of statuses (ADR-0019, K3).
func TestComputeFlow(t *testing.T) {
	msk := time.FixedZone("MSK", 3*3600)
	at := func(day, hour int) time.Time { return time.Date(2026, 10, day, hour, 0, 0, 0, msk) }
	now := at(22, 12)                                 // Thursday
	since := time.Date(2026, 10, 16, 0, 0, 0, 0, msk) // Friday: a period of 7 days

	task := func(n int64, status string, created, changed time.Time, closed bool) store.FlowTasksRow {
		r := store.FlowTasksRow{ID: uuid.NewSHA1(uuid.Nil, []byte{byte(n)}), Number: n, Title: "Задача", Status: status, CreatedAt: created, StatusChangedAt: changed}
		if closed {
			r.ClosedAt = &changed
		}
		return r
	}
	done := task(1, StatusDone, at(10, 9), at(20, 10), true)          // in progress since the 18th, done on the 20th
	quick := task(2, StatusDone, at(17, 9), at(17, 15), true)         // straight from open to done: no cycle
	working := task(3, StatusInProgress, at(14, 9), at(19, 9), false) // in progress since the 19th
	waiting := task(4, StatusOpen, at(1, 9), at(1, 9), false)         // open from before the period
	fresh := task(5, StatusReview, at(21, 9), at(22, 9), false)       // came on the 21st, in review today
	old := task(6, StatusDone, at(1, 9), at(5, 9), true)              // done before the period: counted nowhere
	history := map[uuid.UUID][]statusChange{
		done.ID:    {{at: at(18, 10), from: StatusOpen, to: StatusInProgress}, {at: at(20, 10), from: StatusInProgress, to: StatusDone}},
		quick.ID:   {{at: at(17, 15), from: StatusOpen, to: StatusDone}},
		working.ID: {{at: at(19, 9), from: StatusOpen, to: StatusInProgress}},
		fresh.ID:   {{at: at(21, 12), from: StatusOpen, to: StatusInProgress}, {at: at(22, 9), from: StatusInProgress, to: StatusReview}},
		old.ID:     {{at: at(5, 9), from: StatusOpen, to: StatusDone}},
	}
	f := computeFlow([]store.FlowTasksRow{done, quick, working, waiting, fresh, old}, history, since, now)

	if len(f.CFD) != 7 || !f.CFD[0].Day.Equal(since) || !f.Until.Equal(at(22, 0)) {
		t.Fatalf("days: %d from %v to %v", len(f.CFD), f.CFD[0].Day, f.Until)
	}
	want := map[int]FlowDay{
		0: {Open: 3},                                    // the 16th: done, working, waiting are open; old was done before the period
		1: {Open: 3, Done: 1},                           // the 17th: quick done
		3: {Open: 1, InProgress: 2, Done: 1},            // the 19th: done and working in progress
		4: {Open: 1, InProgress: 1, Done: 2},            // the 20th: done
		6: {Open: 1, InProgress: 1, Review: 1, Done: 2}, // today
	}
	for i, w := range want {
		got := f.CFD[i]
		w.Day = got.Day
		if got != w {
			t.Errorf("day %d: %+v, want %+v", i, got, w)
		}
	}
	if len(f.Cycle) != 1 || f.Cycle[0].Number != 1 || f.Cycle[0].Days != 2 || f.CycleP50 == nil || *f.CycleP50 != 2 || *f.CycleP85 != 2 {
		t.Fatalf("cycle: %+v %v", f.Cycle, f.CycleP50)
	}
	// Weeks from Monday the 12th: quick in the first, done in the week of the 19th.
	if len(f.Throughput) != 2 || f.Throughput[0].Week.Day() != 12 || f.Throughput[0].Done != 1 || f.Throughput[1].Done != 1 {
		t.Fatalf("throughput: %+v", f.Throughput)
	}
	if len(f.Aging) != 3 || f.Aging[0].Number != 4 || f.Aging[0].Days != 21.1 || f.Aging[2].Number != 5 {
		t.Fatalf("aging: %+v", f.Aging)
	}

	if f := computeFlow(nil, nil, since, now); f.CycleP50 != nil || len(f.Cycle) != 0 || len(f.CFD) != 7 || len(f.Aging) != 0 {
		t.Fatalf("empty: %+v", f)
	}
}

func TestPercentile(t *testing.T) {
	v := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if p := percentile(v, 0.5); *p != 5 {
		t.Errorf("p50 %v", *p)
	}
	if p := percentile(v, 0.85); *p != 9 {
		t.Errorf("p85 %v", *p)
	}
	if p := percentile([]float64{3}, 0.85); *p != 3 {
		t.Errorf("one %v", *p)
	}
}
