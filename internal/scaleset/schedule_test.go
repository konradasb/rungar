// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/events"
	"github.com/konradasb/rungar/internal/types"
)

// Times either side of the window newScheduled's scale sets have: Monday
// 2026-10-05, 08:00 to 19:00 UTC.
var (
	beforeWindow = time.Date(2026, 10, 5, 7, 59, 0, 0, time.UTC)
	inWindow     = time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	afterWindow  = time.Date(2026, 10, 5, 19, 0, 0, 0, time.UTC)
)

// newScheduled returns a scale set of min_runners 0, and 3 on weekdays from
// 08:00 to 19:00 UTC, whose clock reads *now.
func newScheduled(t *testing.T, now *time.Time) (*scaleSet, *fakeScaleSetProviders, *fakeMetrics) {
	t.Helper()

	s, f, rec := newScaling(t, 10, 0, 10)

	days, err := types.ParseWeekdays("mon-fri")
	if err != nil {
		t.Fatal(err)
	}
	s.spec.Schedule = types.Schedule{Windows: []types.ScheduleWindow{
		{Days: days, From: 8 * 60, To: 19 * 60, MinRunners: 3},
	}}
	s.now = func() time.Time { return *now }

	return s, f, rec
}

// TestScheduleWindowOpeningCreatesRunners checks runners are created when a
// window opens, with no job to prompt it: a reconciliation retrying the last
// decision is enough.
func TestScheduleWindowOpeningCreatesRunners(t *testing.T) {
	now := beforeWindow
	s, f, rec := newScheduled(t, &now)
	ctx := context.Background()

	if _, err := s.HandleDesiredRunnerCount(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 0 {
		t.Fatalf("created %d runners before the window, want none", len(f.created))
	}

	now = inWindow
	s.retry(ctx)

	if len(f.created) != 3 {
		t.Errorf("created %d runners once the window opened, want its 3", len(f.created))
	}
	if !slices.Equal(rec.minRunners, []int{0, 3}) {
		t.Errorf("min_runners recorded as %v, want [0 3]", rec.minRunners)
	}
}

// TestScheduleWindowClosingRemovesIdleRunners checks the runners a window
// kept are removed once it closes, but not one running a job.
func TestScheduleWindowClosingRemovesIdleRunners(t *testing.T) {
	now := inWindow
	s, f, _ := newScheduled(t, &now)
	ctx := context.Background()

	if _, err := s.HandleDesiredRunnerCount(ctx, 0); err != nil {
		t.Fatal(err)
	}
	busy := s.sortedRunners()[0].Name
	s.markBusy(busy, types.Job{ID: "job"})

	now = afterWindow
	s.retry(ctx)

	left := s.sortedRunners()
	if len(left) != 1 || left[0].Name != busy {
		t.Errorf("left %v, want only the busy runner %s", left, busy)
	}
	if len(f.deleted) != 2 {
		t.Errorf("deleted %v, want the 2 idle runners", f.deleted)
	}
}

// TestScheduleChangeIsRecorded checks a change of min_runners is recorded as
// an event, and the first decision, with nothing to change from, is not.
func TestScheduleChangeIsRecorded(t *testing.T) {
	now := beforeWindow
	s, _, _ := newScheduled(t, &now)
	ctx := context.Background()

	if _, err := s.HandleDesiredRunnerCount(ctx, 0); err != nil {
		t.Fatal(err)
	}
	s.retry(ctx)
	if got := recordedBy(t, s.events); len(got) != 0 {
		t.Fatalf("recorded %v with min_runners unchanged, want nothing", got)
	}

	now = inWindow
	s.retry(ctx)

	var changes []events.Event
	for _, e := range recordedBy(t, s.events) {
		if e.Action == events.ActionMinRunnersChanged {
			changes = append(changes, e)
		}
	}
	if len(changes) != 1 {
		t.Fatalf("recorded %d min_runners changes, want 1", len(changes))
	}

	e := changes[0]
	switch {
	case e.Kind != events.KindScaleSet || e.ScaleSet != s.spec.Name:
		t.Errorf("event = %+v, want one of scale set %s", e, s.spec.Name)
	case e.Attributes["from"] != "0" || e.Attributes["to"] != "3" || e.Attributes["window"] != "mon-fri 08:00-19:00":
		t.Errorf("attributes = %v, want from 0 to 3 in window mon-fri 08:00-19:00", e.Attributes)
	}
}

func TestPausedScaleSetIgnoresItsSchedule(t *testing.T) {
	now := inWindow
	s, f, _ := newScheduled(t, &now)
	s.setPaused(true)

	if got, _ := s.HandleDesiredRunnerCount(context.Background(), 0); got != 0 || len(f.created) != 0 {
		t.Errorf("returned %d having created %d runners, want none while paused", got, len(f.created))
	}
}

func TestStatusReportsTheWindowInForce(t *testing.T) {
	now := inWindow
	s, _, _ := newScheduled(t, &now)

	st := s.status().Status
	if st.MinRunners != 3 || !st.InWindow || st.Window != 0 {
		t.Errorf("min_runners %d, window %d (in one: %t); want 3 in window 0", st.MinRunners, st.Window, st.InWindow)
	}

	now = afterWindow
	st = s.status().Status
	if st.MinRunners != 0 || st.InWindow {
		t.Errorf("min_runners %d, in a window: %t; want 0 outside every window", st.MinRunners, st.InWindow)
	}
}
