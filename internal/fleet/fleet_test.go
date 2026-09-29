// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package fleet

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/konradasb/rungar/internal/types"
)

// TestProbeAsksNothing checks Probe is what placement has learnt of each
// provider, without calling any: reachability from its last listing, its
// runners from the counter and those being made.
func TestProbeAsksNothing(t *testing.T) {
	a, b := &fake{}, &fake{listErr: errors.New("connection refused")}
	m, _ := testFleet(t, Options{},
		Provider{Name: "a", Type: "dicer", Weight: 2, MaxRunners: 5, Backend: a},
		Provider{Name: "b", Type: "dicer", Weight: 1, Backend: b})
	m.CountRunnersWith(func() map[string]int { return map[string]int{"a": 3} })

	before := m.Probe([]string{"a", "b", "c"})
	if !before[0].Reachable || !before[1].Reachable {
		t.Errorf("Probe() = %+v, want providers never asked taken as reachable", before)
	}
	if before[2].Name != "c" || before[2].Err != "no such provider" {
		t.Errorf("Probe() of a provider not in the fleet = %+v", before[2])
	}

	_, _ = m.List(context.Background(), []string{"a", "b"}, nil)
	release, _ := m.byName["a"].claim(func() int { return 3 })
	defer release()

	created, listed := a.calls()
	s := m.Probe([]string{"a", "b"})
	if again, listedAgain := a.calls(); len(again) != len(created) || listedAgain != listed {
		t.Error("Probe() called a provider")
	}

	if got := s[0]; got.Name != "a" || got.Type != "dicer" || got.Weight != 2 || !got.Reachable ||
		got.MaxRunners != 5 || got.RunnerCount != 4 || !got.Usable() {
		t.Errorf("a = %+v, want 3 runners and 1 being made of 5", got)
	}
	if got := s[1]; got.Reachable || got.Err != "connection refused" || got.Usable() {
		t.Errorf("b = %+v, want it unreachable, saying why", got)
	}

	b.mu.Lock()
	b.listErr = nil
	b.mu.Unlock()
	_, _ = m.List(context.Background(), []string{"b"}, nil)
	if got := m.Probe([]string{"b"})[0]; !got.Reachable || got.Err != "" {
		t.Errorf("b = %+v, want it reachable again once it lists", got)
	}
}

func TestListDeleteAndClose(t *testing.T) {
	a, b := &fake{}, &fake{listErr: errors.New("connection refused")}
	a.machines = []types.Machine{
		{Name: "r1", Labels: types.RunnerLabels("i", "set", "r1", "")},
		{Name: "x", Labels: map[string]string{"app": "web"}},
	}
	m, _ := testFleet(t, Options{}, Provider{Name: "a", Backend: a}, Provider{Name: "b", Backend: b})
	ctx := context.Background()

	machines, err := m.List(ctx, []string{"a", "b"}, types.ScaleSetSelector("i", "set"))

	var unreachable *UnreachableError
	if !errors.As(err, &unreachable) || !unreachable.Unreachable("b") || unreachable.Unreachable("a") {
		t.Fatalf("List() = %v, want b named unreachable", err)
	}
	if len(machines) != 1 || machines[0].Name != "r1" || machines[0].Provider != "a" {
		t.Errorf("List() = %+v, want r1 on a, all the same", machines)
	}

	if err := m.Delete(ctx, "a", "r1"); err != nil || len(a.deleted) != 1 || len(b.deleted) != 0 {
		t.Errorf("Delete() = %v; a deleted %v, b deleted %v", err, a.deleted, b.deleted)
	}
	if err := m.Delete(ctx, "nowhere", "r1"); err == nil {
		t.Error("Delete() on a provider not in the fleet = nil, want an error")
	}
	if err := m.Close(); err != nil || !a.closed || !b.closed {
		t.Errorf("Close() = %v; closed a %v, b %v", err, a.closed, b.closed)
	}
}

// TestSetDisabled checks a provider can be taken out of placement while the
// fleet is in use, and put back.
func TestSetDisabled(t *testing.T) {
	m, _ := testFleet(t, Options{}, Provider{Name: "a", Backend: &fake{}})

	if err := m.SetDisabled("a", true); err != nil {
		t.Fatal(err)
	}
	if state := m.Probe([]string{"a"})[0]; !state.Disabled || state.Usable() {
		t.Errorf("a = %+v, want it disabled and out of placement", state)
	}

	if err := m.SetDisabled("a", false); err != nil {
		t.Fatal(err)
	}
	if state := m.Probe([]string{"a"})[0]; state.Disabled {
		t.Errorf("a = %+v, want it enabled again", state)
	}

	if err := m.SetDisabled("nowhere", true); err == nil {
		t.Error("SetDisabled() of a provider not in the fleet = nil, want an error")
	}
}

func TestNamesKeepsTheConfigurationsOrder(t *testing.T) {
	m, _ := testFleet(t, Options{}, Provider{Name: "b", Backend: &fake{}}, Provider{Name: "a", Backend: &fake{}})

	if names, err := m.Names(""); err != nil || strings.Join(names, ",") != "b,a" {
		t.Errorf("Names() = %v, %v; want b,a", names, err)
	}
	if names, err := m.Names("a"); err != nil || len(names) != 1 {
		t.Errorf("Names(a) = %v, %v; want a", names, err)
	}
	if _, err := m.Names("c"); err == nil {
		t.Error("Names(c) = nil, want an error naming the providers there are")
	}
}

func TestNewFillsDefaultOptions(t *testing.T) {
	m := New([]Provider{{Name: "a"}}, Options{}).byName["a"]

	if m.backoffFirst != defaultBackoffFirst || m.backoffMax != defaultBackoffMax || m.holdDown != defaultHoldDown {
		t.Errorf("New() with no options = %v/%v %v, want the defaults", m.backoffFirst, m.backoffMax, m.holdDown)
	}
}

// TestAnsweringIsLoggedWhenItChanges checks a provider that stops answering
// is warned of once, and said to answer again once it does, and that a call
// its caller gave up on changes nothing.
func TestAnsweringIsLoggedWhenItChanges(t *testing.T) {
	var buf bytes.Buffer
	m := New([]Provider{{Name: "compute2"}, {Name: "compute3"}},
		Options{Logger: slog.New(slog.NewTextHandler(&buf, nil))})
	mem := m.byName["compute2"]

	mem.answered(nil)
	mem.answered(errors.New("connection refused"))
	mem.answered(errors.New("connection refused"))
	mem.answered(context.Canceled)
	mem.answered(nil)
	mem.answered(nil)

	out := buf.String()
	if n := strings.Count(out, "not answering"); n != 1 {
		t.Errorf("warned %d times that it is not answering, want once:\n%s", n, out)
	}
	if n := strings.Count(out, "answering again"); n != 1 {
		t.Errorf("said %d times that it answers again, want once:\n%s", n, out)
	}
	if !strings.Contains(out, "provider=compute2") || !strings.Contains(out, "connection refused") {
		t.Errorf("the log does not name the provider and why:\n%s", out)
	}
	if strings.Index(out, "not answering") > strings.Index(out, "answering again") {
		t.Errorf("the log is out of order:\n%s", out)
	}

	buf.Reset()
	m.byName["compute3"].answered(errors.New("connection refused"))

	if !strings.Contains(buf.String(), "not answering") {
		t.Error("a provider down from the start is not warned of")
	}
}
