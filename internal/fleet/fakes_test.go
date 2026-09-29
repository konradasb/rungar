// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package fleet

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/events"
	"github.com/konradasb/rungar/internal/types"
)

// fake is a provider that answers as it is told, and remembers what it was
// asked. The fleet is backend-agnostic, so what is under test is what it does
// with any provider's answers.
type fake struct {
	mu sync.Mutex

	// creates are what Create returns, one a call, the last repeated; none
	// takes every runner.
	creates []error
	// gate, when set, is waited on by Create before it answers, and entered
	// is told when a Create is waiting.
	gate    chan struct{}
	entered chan struct{}

	deleteErr error
	listErr   error
	machines  []types.Machine

	created []string
	deleted []string
	listed  int
	closed  bool
}

func (f *fake) List(_ context.Context, selector map[string]string) ([]types.Machine, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.listed++
	if f.listErr != nil {
		return nil, f.listErr
	}

	var out []types.Machine
	for _, m := range f.machines {
		if types.HasLabels(m.Labels, selector) {
			out = append(out, m)
		}
	}

	return out, nil
}

func (f *fake) Create(ctx context.Context, spec types.MachineSpec) error {
	f.mu.Lock()
	gate, entered := f.gate, f.entered
	f.mu.Unlock()

	if gate != nil {
		if entered != nil {
			entered <- struct{}{}
		}
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.created = append(f.created, spec.Name)

	if len(f.creates) == 0 {
		return nil
	}
	err := f.creates[0]
	if len(f.creates) > 1 {
		f.creates = f.creates[1:]
	}

	return err
}

func (f *fake) Delete(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.deleted = append(f.deleted, name)

	return f.deleteErr
}

func (f *fake) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.closed = true

	return nil
}

// refuse has every Create from now on return err.
func (f *fake) refuse(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.creates = []error{err}
}

// requests returns the names Create was called with, and how often List was.
func (f *fake) requests() (created []string, listed int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.created...), f.listed
}

// runner is a runner spec that is only its description.
type runner string

func (r runner) Describe() string { return string(r) }

// scaleSet returns a scale set of this priority and placement on providers, in
// that order, whose runner on each is described "<scale set>@<provider>".
func scaleSet(name string, priority int, placement types.Placement, providers ...string) types.ScaleSetSpec {
	spec := types.ScaleSetSpec{
		Name:        name,
		Priority:    priority,
		Placement:   placement,
		RunnerSpecs: map[string]types.RunnerSpec{},
	}
	for _, p := range providers {
		spec.Providers = append(spec.Providers, types.ProviderRef{Name: p})
		spec.RunnerSpecs[p] = runner(name + "@" + p)
	}

	return spec
}

// machine returns the spec of runner name, the same on every provider.
func machine(name string) func(string) types.MachineSpec {
	return func(string) types.MachineSpec { return types.MachineSpec{Name: name} }
}

// clock is a time a test moves by hand, so that nothing here waits.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock {
	return &clock{now: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)}
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// testFleet returns a fleet of the providers, on a hand-wound clock: skipped
// by a scale set for ten seconds after a first refusal and forty at most, and
// held for a minute.
func testFleet(t *testing.T, cfg Config, providers ...ProviderConfig) (*Manager, *clock) {
	t.Helper()

	if cfg.BackoffFirst == 0 {
		cfg.BackoffFirst, cfg.BackoffMax = 10*time.Second, 40*time.Second
	}
	if cfg.HoldDown == 0 {
		cfg.HoldDown = time.Minute
	}

	m := New(providers, cfg)
	c := newClock()
	for _, each := range m.members {
		each.now = c.Now
	}

	return m, c
}

// recorder keeps the events it is told of.
type recorder struct {
	mu     sync.Mutex
	events []events.Event
}

func (r *recorder) Record(e events.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.events = append(r.events, e)
}

func (r *recorder) all() []events.Event {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]events.Event(nil), r.events...)
}
