package api

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/tasks"
)

// The board's cursor carries the rank, the time and the id of the last task;
// anything but its own spelling is not one.
func TestFlowCursor(t *testing.T) {
	k := tasks.FlowKey{Rank: 2, Changed: time.Date(2026, 11, 17, 9, 30, 0, 123456000, time.UTC), ID: uuid.MustParse("01a1e60e-2343-7140-ab43-b9e794af2641")}
	c := flowCursor(k)
	got, err := parseFlowCursor(c)
	if err != nil || *got != k {
		t.Fatalf("round trip %q: %+v %v", c, got, err)
	}
	for _, bad := range []string{
		"", "f", "f2.1", "2.1." + k.ID.String(), "f4.1." + k.ID.String(), "f-1.1." + k.ID.String(),
		"f2.x." + k.ID.String(), "f2.1.42", "f02.1." + k.ID.String(), c + "x",
		"f2.1." + "01A1E60E-2343-7140-AB43-B9E794AF2641",
	} {
		if _, err := parseFlowCursor(bad); err == nil {
			t.Errorf("parseFlowCursor(%q) accepted", bad)
		}
	}
}
