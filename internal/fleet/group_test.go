// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package fleet

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

var (
	errFull    = errdefs.NoCapacity("not enough memory")
	errInvalid = errdefs.InvalidArgument("8 vCPUs is more than the host's 4 CPUs")
	errFailing = errors.New("connection reset")
)

func TestOrder(t *testing.T) {
	tests := []struct {
		name      string
		weights   []float64
		runners   map[string]int
		placement types.Placement
		want      string
	}{
		{"pack keeps the scale set's order", []float64{1, 1, 1}, nil, types.PlacementPack, "abc"},
		{"pack tries the heaviest first", []float64{1, 2, 2}, nil, types.PlacementPack, "bca"},
		{"pack ignores the runners", []float64{1, 1, 1}, map[string]int{"a": 5}, types.PlacementPack, "abc"},
		{"spread tries the emptiest first", []float64{1, 1, 1}, map[string]int{"a": 2, "b": 1}, types.PlacementSpread, "cba"},
		{"spread counts weight", []float64{2, 1, 1}, map[string]int{"a": 2, "b": 1, "c": 2}, types.PlacementSpread, "abc"},
		{"spread keeps the order on ties", []float64{1, 1, 1}, nil, types.PlacementSpread, "abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var members []*member
			for i, name := range []string{"a", "b", "c"} {
				members = append(members, &member{name: name, weight: tt.weights[i]})
			}

			order(members, tt.runners, tt.placement)

			var got strings.Builder
			for _, m := range members {
				got.WriteString(m.name)
			}
			if got.String() != tt.want {
				t.Errorf("order = %s, want %s", got.String(), tt.want)
			}
		})
	}
}

func TestPlaceTakesTheFirstThatTakesIt(t *testing.T) {
	a, b := &fake{}, &fake{}
	m, _ := testFleet(t, Options{}, Provider{Name: "a", Weight: 1, Backend: a}, Provider{Name: "b", Weight: 1, Backend: b})

	on, release, err := m.Group(scaleSet("s", 0, types.PlacementPack, "a", "b")).Place(context.Background(), nil, machine("r1"))
	if err != nil || on != "a" {
		t.Fatalf("Place() = %q, %v; want a", on, err)
	}
	release()

	if created, _ := b.calls(); len(created) != 0 {
		t.Errorf("b was asked for %v, want nothing once a took the runner", created)
	}
}

// TestAFullProviderIsSkippedByTheScaleSetThatFoundIt checks a provider that is
// full makes Place try the next one, and is skipped for a while by the scale
// set that found it full, but not by another.
func TestAFullProviderIsSkippedByTheScaleSetThatFoundIt(t *testing.T) {
	a, b := &fake{creates: []error{errFull}}, &fake{}
	m, c := testFleet(t, Options{}, Provider{Name: "a", Weight: 1, Backend: a}, Provider{Name: "b", Weight: 1, Backend: b})
	ctx := context.Background()
	s1 := m.Group(scaleSet("s1", 0, types.PlacementPack, "a", "b"))

	on, _, err := s1.Place(ctx, nil, machine("r1"))
	if err != nil || on != "b" {
		t.Fatalf("Place() = %q, %v; want b, after a was full", on, err)
	}
	if len(a.deleted) != 1 || a.deleted[0] != "r1" {
		t.Errorf("a deleted %v, want the machine it may have left, r1", a.deleted)
	}

	a.refuse(nil)
	if on, _, _ := s1.Place(ctx, nil, machine("r2")); on != "b" {
		t.Errorf("s1 placed on %q, want b while it skips a as full", on)
	}
	if on, _, _ := m.Group(scaleSet("s2", 0, types.PlacementPack, "a", "b")).Place(ctx, nil, machine("r3")); on != "a" {
		t.Errorf("s2 placed on %q, want a, which only s1 found full", on)
	}

	c.advance(10 * time.Second)
	if on, _, _ := s1.Place(ctx, nil, machine("r4")); on != "a" {
		t.Errorf("s1 placed on %q, want a again once its time is up", on)
	}
}

func TestBackoffDoublesAndIsForgottenOnceMade(t *testing.T) {
	m, _ := testFleet(t, Options{}, Provider{Name: "a", Backend: &fake{}})
	a := m.byName["a"]

	var got []time.Duration
	for _, err := range []error{errFull, errFailing, errFull, errFailing} {
		wait, _, _ := a.recordFailure("s", err)
		got = append(got, wait)
	}
	want := []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second, 40 * time.Second}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("skipped for %v, want %v whether full or failing", got, want)
			break
		}
	}

	a.recordSuccess("s")
	if wait, failures, _ := a.recordFailure("s", errFailing); wait != 10*time.Second || failures != 1 {
		t.Errorf("after a runner was made, skipped for %v after %d failures, want the first 10s again", wait, failures)
	}
}

// TestAFailingProviderIsSkippedByTheScaleSetThatFoundIt checks a provider that
// fails to make a runner, however it says so, is skipped for a while by that
// scale set alone, and tried by every other.
func TestAFailingProviderIsSkippedByTheScaleSetThatFoundIt(t *testing.T) {
	for _, refusal := range []error{errFailing, errInvalid} {
		t.Run(refusal.Error(), func(t *testing.T) {
			a, b := &fake{creates: []error{refusal, nil}}, &fake{}
			m, c := testFleet(t, Options{}, Provider{Name: "a", Weight: 1, Backend: a}, Provider{Name: "b", Weight: 1, Backend: b})
			ctx := context.Background()
			s1 := m.Group(scaleSet("s1", 0, types.PlacementPack, "a", "b"))

			if on, _, err := s1.Place(ctx, nil, machine("r1")); err != nil || on != "b" {
				t.Fatalf("Place() = %q, %v; want b after a failed", on, err)
			}
			if on, _, _ := s1.Place(ctx, nil, machine("r2")); on != "b" {
				t.Errorf("s1 placed on %q, want b while it skips a", on)
			}
			if on, _, _ := m.Group(scaleSet("s2", 0, types.PlacementPack, "a", "b")).Place(ctx, nil, machine("r3")); on != "a" {
				t.Errorf("s2 placed on %q, want a, which only failed s1", on)
			}
			if s := m.Probe([]string{"a"})[0]; !s.Usable() {
				t.Errorf("a = %+v, want it usable: one scale set's refusal is not the provider's", s)
			}

			c.advance(10 * time.Second)
			if on, _, _ := s1.Place(ctx, nil, machine("r4")); on != "a" {
				t.Errorf("s1 placed on %q, want a again once its time is up", on)
			}
		})
	}
}

// TestAFailureIsReportedForTheScaleSet checks how long a scale set skips a
// provider that failed it, how many times in a row, and why, until it makes
// the scale set's runner.
func TestAFailureIsReportedForTheScaleSet(t *testing.T) {
	a, b := &fake{creates: []error{errFailing}}, &fake{}
	m, c := testFleet(t, Options{}, Provider{Name: "a", Weight: 1, Backend: a}, Provider{Name: "b", Weight: 1, Backend: b})
	ctx := context.Background()
	s1 := m.Group(scaleSet("s1", 0, types.PlacementPack, "a", "b"))

	s1.Place(ctx, nil, machine("r1"))
	if s := m.byName["a"].scaleSetState("s1"); s.BackoffFor != 10*time.Second || s.Failures != 1 ||
		s.Failure != "connection reset" || s.Full {
		t.Errorf("a for s1 = %+v, want it skipped for 10s after 1 failure, saying why", s)
	}

	c.advance(10 * time.Second)
	s1.Place(ctx, nil, machine("r2"))
	if s := m.byName["a"].scaleSetState("s1"); s.BackoffFor != 20*time.Second || s.Failures != 2 {
		t.Errorf("a for s1 = %+v, want 20s after 2 failures", s)
	}

	c.advance(20 * time.Second)
	a.refuse(nil)
	if on, _, _ := s1.Place(ctx, nil, machine("r3")); on != "a" {
		t.Fatalf("placed on %q, want a once its time is up", on)
	}
	if s := m.byName["a"].scaleSetState("s1"); s.BackoffFor != 0 || s.Failures != 0 || s.Failure != "" {
		t.Errorf("a for s1 = %+v, want it forgiven once it made a runner", s)
	}
}

func TestBackoffDoublesToItsCapWithoutOverflowing(t *testing.T) {
	m, _ := testFleet(t, Options{}, Provider{Name: "a", Backend: &fake{}})
	a := m.byName["a"]

	var got []time.Duration
	for range 500 {
		wait, _, _ := a.recordFailure("s", errFailing)
		got = append(got, wait)
	}

	for i, want := range []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second} {
		if got[i] != want {
			t.Errorf("failure %d skipped for %v, want %v", i+1, got[i], want)
		}
	}
	for _, d := range got {
		if d <= 0 || d > 40*time.Second {
			t.Fatalf("skipped for %v, want it between zero and the cap", d)
		}
	}
}

// TestAMachineThatCannotBeRemovedStopsPlacement checks a provider that failed
// and left a machine it cannot remove is returned, and nothing else is tried
// under the runner's name.
func TestAMachineThatCannotBeRemovedStopsPlacement(t *testing.T) {
	a := &fake{creates: []error{errFailing}, deleteErr: errors.New("timeout")}
	b := &fake{}
	m, _ := testFleet(t, Options{}, Provider{Name: "a", Weight: 1, Backend: a}, Provider{Name: "b", Weight: 1, Backend: b})

	on, release, err := m.Group(scaleSet("s", 0, types.PlacementPack, "a", "b")).Place(context.Background(), nil, machine("r1"))
	if on != "a" || release != nil || err == nil {
		t.Fatalf("Place() = %q, %v, %v; want a and the error", on, release != nil, err)
	}
	if !strings.Contains(err.Error(), "connection reset") || !strings.Contains(err.Error(), "timeout") {
		t.Errorf("error %q does not say both why it failed and why it was not removed", err)
	}
	if created, _ := b.calls(); len(created) != 0 {
		t.Errorf("b was asked for %v, want nothing", created)
	}
}

// TestACancelledPlacementTriesNothingElse checks a caller that gives up is not
// tried on the next provider, and says nothing of the provider it was trying:
// it is neither skipped nor recorded.
func TestACancelledPlacementTriesNothingElse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	a := &fake{gate: make(chan struct{}), entered: make(chan struct{}, 1)}
	b := &fake{}
	rec := &recorder{}
	m, _ := testFleet(t, Options{Events: rec},
		Provider{Name: "a", Weight: 1, Backend: a}, Provider{Name: "b", Weight: 1, Backend: b})

	go func() {
		<-a.entered
		cancel()
	}()

	on, _, err := m.Group(scaleSet("s", 0, types.PlacementPack, "a", "b")).Place(ctx, nil, machine("r1"))
	if on != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("Place() = %q, %v; want the cancellation", on, err)
	}
	if created, _ := b.calls(); len(created) != 0 {
		t.Errorf("b was asked for %v after the caller gave up", created)
	}
	if len(a.deleted) != 1 {
		t.Errorf("a deleted %v, want the machine it may have left removed all the same", a.deleted)
	}
	if s := m.byName["a"].scaleSetState("s"); s.Failures != 0 {
		t.Errorf("a for s = %+v, want it untouched by a caller giving up", s)
	}
	if events := rec.all(); len(events) != 0 {
		t.Errorf("recorded %+v, want nothing", events)
	}
}

// TestARunnerOutOfTimeTriesNothingElse checks a runner whose time to start runs
// out is not tried on the next provider, and skips the provider that took too
// long.
func TestARunnerOutOfTimeTriesNothingElse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	a := &fake{gate: make(chan struct{})}
	b := &fake{}
	m, _ := testFleet(t, Options{}, Provider{Name: "a", Weight: 1, Backend: a}, Provider{Name: "b", Weight: 1, Backend: b})

	on, _, err := m.Group(scaleSet("s", 0, types.PlacementPack, "a", "b")).Place(ctx, nil, machine("r1"))
	if on != "" || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Place() = %q, %v; want the deadline", on, err)
	}
	if created, _ := b.calls(); len(created) != 0 {
		t.Errorf("b was asked for %v after the runner ran out of time", created)
	}
	if s := m.byName["a"].scaleSetState("s"); s.BackoffFor <= 0 || s.Failures != 1 {
		t.Errorf("a for s = %+v, want it skipped for taking too long", s)
	}
}

func TestNoProviderTakingTheRunnerSaysWhyOfEach(t *testing.T) {
	a, b := &fake{creates: []error{errFull}}, &fake{creates: []error{errFailing}}
	m, _ := testFleet(t, Options{}, Provider{Name: "a", Weight: 1, Backend: a}, Provider{Name: "b", Weight: 1, Backend: b})

	_, _, err := m.Group(scaleSet("s", 0, types.PlacementPack, "a", "b")).Place(context.Background(), nil, machine("r1"))
	if !errors.Is(err, errdefs.ErrNoCapacity) {
		t.Fatalf("Place() = %v, want no capacity", err)
	}
	for _, want := range []string{"a: not enough memory", "b: connection reset"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not say %q", err, want)
		}
	}
}

// TestCanPlace checks CanPlace says why when there is no provider to try, for
// each reason one is not, and asks no provider anything.
func TestCanPlace(t *testing.T) {
	tests := []struct {
		name  string
		setup func(m *Manager, b *fake)
		want  string
	}{
		{"one to try", func(*Manager, *fake) {}, ""},
		{"disabled", func(m *Manager, _ *fake) { _ = m.SetDisabled("a", true) }, "disabled"},
		{"not answering", func(m *Manager, _ *fake) { m.byName["a"].answered(errFailing) }, "not answering"},
		{"full", func(m *Manager, _ *fake) { m.byName["a"].recordFailure("s", errFull) }, "full"},
		{"failing", func(m *Manager, _ *fake) {
			m.byName["a"].recordFailure("s", errFailing)
		}, "failing to make its runner: connection reset"},
		{"failing for another scale set", func(m *Manager, _ *fake) {
			m.byName["a"].recordFailure("other", errFailing)
		}, ""},
		{"at its limit", func(m *Manager, _ *fake) {
			m.CountRunnersWith(func() map[string]int { return map[string]int{"a": 2} })
		}, "at its limit of 2 runners"},
		{"held", func(m *Manager, _ *fake) { m.byName["a"].claimHold(10, "big") }, `held for scale set big`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &fake{}
			m, _ := testFleet(t, Options{}, Provider{Name: "a", Weight: 1, MaxRunners: 2, Backend: a})
			tt.setup(m, a)

			err := m.Group(scaleSet("s", 0, types.PlacementPack, "a")).CanPlace()

			switch {
			case tt.want == "" && err != nil:
				t.Errorf("CanPlace() = %v, want nil", err)
			case tt.want != "" && (!errors.Is(err, errdefs.ErrNoCapacity) || !strings.Contains(err.Error(), tt.want)):
				t.Errorf("CanPlace() = %v, want no capacity saying %q", err, tt.want)
			}
			if created, listed := a.calls(); len(created) != 0 || listed != 0 {
				t.Errorf("CanPlace() asked the provider for %v and listed it %d times", created, listed)
			}
		})
	}

	m, _ := testFleet(t, Options{})
	if err := m.Group(scaleSet("s", 0, types.PlacementPack)).CanPlace(); !errors.Is(err, errdefs.ErrNoCapacity) {
		t.Errorf("CanPlace() of a scale set with no providers = %v, want no capacity", err)
	}
}

// TestALimitCountsTheScaleSetsRunnersAndThoseBeingMade checks max_runners holds
// what the counter says is on a provider, with the runners being made there,
// and that releasing one frees its place.
func TestALimitCountsTheScaleSetsRunnersAndThoseBeingMade(t *testing.T) {
	var (
		mu     sync.Mutex
		onA    int
		ctx    = context.Background()
		b      = &fake{}
		m, _   = testFleet(t, Options{}, Provider{Name: "a", Weight: 2, MaxRunners: 2, Backend: &fake{}}, Provider{Name: "b", Weight: 1, Backend: b})
		g      = m.Group(scaleSet("s", 0, types.PlacementPack, "a", "b"))
		counts = func() map[string]int {
			mu.Lock()
			defer mu.Unlock()

			return map[string]int{"a": onA}
		}
	)
	m.CountRunnersWith(counts)

	on, release, err := g.Place(ctx, nil, machine("r1"))
	if err != nil || on != "a" {
		t.Fatalf("Place() = %q, %v; want a", on, err)
	}
	if s := m.Probe([]string{"a"})[0]; s.RunnerCount != 1 {
		t.Errorf("a has %d runners while one is being made, want 1", s.RunnerCount)
	}

	// Made, and counted as the scale set's.
	mu.Lock()
	onA = 1
	mu.Unlock()
	release()
	release()

	if on, release, _ := g.Place(ctx, nil, machine("r2")); on != "a" {
		t.Fatalf("placed on %q, want a, with room for one more", on)
	} else {
		mu.Lock()
		onA = 2
		mu.Unlock()
		release()
	}

	if s := m.Probe([]string{"a"})[0]; s.RunnerCount != 2 || s.MaxRunners != 2 {
		t.Errorf("a = %+v, want it at its limit of 2", s)
	}
	if on, _, _ := g.Place(ctx, nil, machine("r3")); on != "b" {
		t.Errorf("placed on %q, want b once a is at its limit", on)
	}
}

// TestALimitCountsRunnersUnderTheProvidersLock checks a claim counts the scale
// sets' runners while holding the provider's lock. Counted before it, a runner
// recorded in between is missed and the provider can go over its limit.
func TestALimitCountsRunnersUnderTheProvidersLock(t *testing.T) {
	m, _ := testFleet(t, Options{}, Provider{Name: "a", MaxRunners: 2, Backend: &fake{}})
	mem := m.byName["a"]

	var readLocked bool
	release, ok := mem.claim(func() int {
		// Held by the claim, so not ours to take.
		if mem.mu.TryLock() {
			mem.mu.Unlock()
		} else {
			readLocked = true
		}

		return 1
	})
	if !ok {
		t.Fatal("claim() refused a place under the limit")
	}
	defer release()

	if !readLocked {
		t.Error("claim() read the runners on the provider without holding its lock")
	}

	// One runner on it and one being made: at its limit of 2.
	if _, ok := mem.claim(func() int { return 1 }); ok {
		t.Error("claim() took a place beyond the limit")
	}
}

// TestALimitOfOneMakesOneRunnerAtATime checks runners placed at once on a
// provider with room for one more make one, however they race.
func TestALimitOfOneMakesOneRunnerAtATime(t *testing.T) {
	const placements = 8

	a := &fake{gate: make(chan struct{}), entered: make(chan struct{}, placements)}
	m, _ := testFleet(t, Options{}, Provider{Name: "a", MaxRunners: 1, Backend: a})

	results := make(chan error, placements)
	for i := range placements {
		g := m.Group(scaleSet("s"+string(rune('a'+i)), 0, types.PlacementPack, "a"))
		go func() {
			_, _, err := g.Place(context.Background(), nil, machine("r"))
			results <- err
		}()
	}

	// Every placement but the one making the runner gives up at once.
	for range placements - 1 {
		if err := <-results; !errors.Is(err, errdefs.ErrNoCapacity) {
			t.Errorf("Place() = %v, want no capacity: the one place is taken", err)
		}
	}
	close(a.gate)
	if err := <-results; err != nil {
		t.Errorf("Place() = %v, want the runner made", err)
	}

	if created, _ := a.calls(); len(created) != 1 {
		t.Errorf("made %d runners, want 1", len(created))
	}
}

// TestAHigherPriorityThatFindsAProviderFullHoldsLowerOnesOff checks a scale set
// of higher priority that finds a provider full keeps lower priorities off it,
// so that the room it is waiting for goes to it, until it places or its hold
// runs out.
func TestAHigherPriorityThatFindsAProviderFullHoldsLowerOnesOff(t *testing.T) {
	a, b := &fake{creates: []error{errFull}}, &fake{}
	m, c := testFleet(t, Options{}, Provider{Name: "a", Weight: 1, Backend: a}, Provider{Name: "b", Weight: 1, Backend: b})
	ctx := context.Background()
	big := m.Group(scaleSet("big", 10, types.PlacementPack, "a"))
	small := m.Group(scaleSet("small", 0, types.PlacementPack, "a", "b"))
	peer := m.Group(scaleSet("peer", 10, types.PlacementPack, "a"))

	if _, _, err := big.Place(ctx, nil, machine("r1")); !errors.Is(err, errdefs.ErrNoCapacity) {
		t.Fatalf("Place() = %v, want no capacity: a is full", err)
	}
	a.refuse(nil)

	if reason := small.HoldBackReason(); !strings.Contains(reason, `a for scale set "big"`) {
		t.Errorf("HoldBackReason() = %q, want it to say a is held for big", reason)
	}
	if on, _, _ := small.Place(ctx, nil, machine("r2")); on != "b" {
		t.Errorf("small placed on %q, want b while a is held for big", on)
	}
	if on, _, _ := peer.Place(ctx, nil, machine("r3")); on != "a" {
		t.Errorf("peer placed on %q, want a: a hold does not hold back an equal priority", on)
	}
	if reason := peer.HoldBackReason(); reason != "" {
		t.Errorf("HoldBackReason() of an equal priority = %q, want nothing", reason)
	}

	// peer placing lifts big's hold, being of its priority.
	if on, _, _ := small.Place(ctx, nil, machine("r4")); on != "a" {
		t.Errorf("small placed on %q, want a once the hold is lifted", on)
	}

	// A hold not renewed runs out.
	a.refuse(errFull)
	big.Place(ctx, nil, machine("r5"))
	a.refuse(nil)
	c.advance(time.Minute + time.Second)
	if reason := small.HoldBackReason(); reason != "" {
		t.Errorf("HoldBackReason() after the hold-down = %q, want nothing", reason)
	}
}

// TestFindingNoProviderToTryRenewsTheHold checks a scale set skipping its only
// provider as full keeps lower priorities off it, rather than letting its hold
// run out while it waits.
func TestFindingNoProviderToTryRenewsTheHold(t *testing.T) {
	a := &fake{creates: []error{errFull}}
	m, c := testFleet(t, Options{BackoffFirst: time.Hour, BackoffMax: time.Hour}, Provider{Name: "a", Backend: a})
	big := m.Group(scaleSet("big", 10, types.PlacementPack, "a"))
	small := m.Group(scaleSet("small", 0, types.PlacementPack, "a"))

	big.Place(context.Background(), nil, machine("r1"))
	c.advance(50 * time.Second)
	if err := big.CanPlace(); !errors.Is(err, errdefs.ErrNoCapacity) {
		t.Fatalf("CanPlace() = %v, want no capacity while a is full", err)
	}
	c.advance(50 * time.Second)

	if reason := small.HoldBackReason(); reason == "" {
		t.Error("the hold ran out while big was still waiting for a")
	}
}

// TestAFailingProviderHoldsNoOneOff checks a scale set of higher priority
// that a provider fails, rather than finds full, keeps no one off it: it is
// not waiting for room.
func TestAFailingProviderHoldsNoOneOff(t *testing.T) {
	a := &fake{creates: []error{errFailing, nil}}
	m, _ := testFleet(t, Options{}, Provider{Name: "a", Weight: 1, Backend: a})
	ctx := context.Background()
	big := m.Group(scaleSet("big", 10, types.PlacementPack, "a"))
	small := m.Group(scaleSet("small", 0, types.PlacementPack, "a"))

	if _, _, err := big.Place(ctx, nil, machine("r1")); !errors.Is(err, errdefs.ErrNoCapacity) {
		t.Fatalf("Place() = %v, want no capacity: a failed", err)
	}
	if err := big.CanPlace(); !errors.Is(err, errdefs.ErrNoCapacity) {
		t.Fatalf("CanPlace() = %v, want no capacity while big skips a", err)
	}

	if reason := small.HoldBackReason(); reason != "" {
		t.Errorf("HoldBackReason() = %q, want nothing: failing is not being full", reason)
	}
	if on, _, _ := small.Place(ctx, nil, machine("r2")); on != "a" {
		t.Errorf("small placed on %q, want a", on)
	}
}

// TestEachRefusalIsAnEventOfItsKind checks a full provider and a failing one
// are each recorded, as what they are, once for each runner refused.
func TestEachRefusalIsAnEventOfItsKind(t *testing.T) {
	a := &fake{creates: []error{errFull}}
	b := &fake{creates: []error{errInvalid}}
	c := &fake{creates: []error{errFailing}}
	rec := &recorder{}
	m, _ := testFleet(t, Options{Events: rec},
		Provider{Name: "a", Weight: 1, Backend: a}, Provider{Name: "b", Weight: 1, Backend: b},
		Provider{Name: "c", Weight: 1, Backend: c})

	m.Group(scaleSet("s", 0, types.PlacementPack, "a", "b", "c")).Place(context.Background(), nil, machine("r1"))
	// s skips all three for now.
	m.Group(scaleSet("s", 0, types.PlacementPack, "a", "b", "c")).Place(context.Background(), nil, machine("r2"))
	m.Group(scaleSet("t", 0, types.PlacementPack, "b")).Place(context.Background(), nil, machine("r3"))

	events := rec.all()
	want := []struct {
		provider string
		action   types.EventAction
		scaleSet string
	}{
		{"a", types.ActionFull, "s"},
		{"b", types.ActionFailing, "s"},
		{"c", types.ActionFailing, "s"},
		{"b", types.ActionFailing, "t"},
	}
	if len(events) != len(want) {
		t.Fatalf("recorded %+v, want %d events", events, len(want))
	}
	for i, w := range want {
		e := events[i]
		if e.Kind != types.KindProvider || e.Provider != w.provider || e.Name != w.provider ||
			e.Action != w.action || e.ScaleSet != w.scaleSet {
			t.Errorf("event %d = %+v, want %s %s for %s", i, e, w.provider, w.action, w.scaleSet)
		}
		if e.Attributes["runner"] == "" || e.Attributes["error"] == "" {
			t.Errorf("event %d does not say which runner and why: %+v", i, e.Attributes)
		}
	}

	if got, want := events[2].Message,
		"Failed to make runner r1: connection reset; s tries the next provider, and this one again in 10s"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}

// TestARefusalIsLoggedWithTheScaleSet checks the log says which scale set's
// runner a provider refused, since several share one.
func TestARefusalIsLoggedWithTheScaleSet(t *testing.T) {
	var log bytes.Buffer
	m, _ := testFleet(t, Options{Logger: slog.New(slog.NewTextHandler(&log, nil))},
		Provider{Name: "a", Backend: &fake{creates: []error{errFailing}}})

	m.Group(scaleSet("rungar-c2-m4", 0, types.PlacementPack, "a")).Place(context.Background(), nil, machine("r1"))

	for _, want := range []string{"scale_set=rungar-c2-m4", "provider=a", "runner=r1"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("the warning does not say %s: %q", want, log.String())
		}
	}
}
