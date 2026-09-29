// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package fleet places runners on providers. It tries a scale set's providers
// in order until one makes the runner, and remembers each refusal: a scale set
// skips a provider that refused its runner for a while, and one that said it
// was full is kept for the scale set against lower priorities. It never asks a
// provider whether it has room.
package fleet

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/events"
	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

// Provider is one configured, connected provider.
type Provider struct {
	Name     string
	Type     string
	Weight   float64
	Endpoint string

	// Disabled is whether the configuration keeps the provider out of
	// placement; see Manager.SetDisabled.
	Disabled bool

	// MaxRunners caps the runners of every scale set on the provider. Zero
	// is no limit.
	MaxRunners int

	Backend provider.Provider
}

// Options configure a Manager. Zero durations take the defaults.
type Options struct {
	// BackoffFirst and BackoffMax bound how long a scale set skips a
	// provider that refused its runner.
	BackoffFirst time.Duration
	BackoffMax   time.Duration

	// HoldDown is how long a scale set that found a provider full keeps
	// lower priorities off it.
	HoldDown time.Duration

	// Events records providers found full or failing to make a runner. Nil
	// records nothing.
	Events events.Recorder

	Logger *slog.Logger
}

const (
	defaultBackoffFirst = 15 * time.Second
	defaultBackoffMax   = 2 * time.Minute
	defaultHoldDown     = 30 * time.Second
)

// Manager is every provider the daemon places runners on. It is safe for
// concurrent use.
type Manager struct {
	members []*member
	byName  map[string]*member
	events  events.Recorder
	logger  *slog.Logger

	counterMu sync.RWMutex
	counter   func() map[string]int
}

// New returns a fleet of the providers, in the order given.
func New(providers []Provider, opts Options) *Manager {
	opts = withDefaults(opts)

	m := &Manager{byName: make(map[string]*member, len(providers)), events: opts.Events, logger: opts.Logger}
	for _, p := range providers {
		mem := newMember(p, opts)
		m.members = append(m.members, mem)
		m.byName[p.Name] = mem
	}

	return m
}

// withDefaults fills in the options left unset.
func withDefaults(opts Options) Options {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.Events == nil {
		opts.Events = events.Discard
	}
	if opts.BackoffFirst <= 0 {
		opts.BackoffFirst = defaultBackoffFirst
	}
	if opts.BackoffMax <= 0 {
		opts.BackoffMax = defaultBackoffMax
	}
	if opts.HoldDown <= 0 {
		opts.HoldDown = defaultHoldDown
	}

	return opts
}

// Names returns every provider's name, in the fleet's order, or only, if the
// fleet has it.
func (m *Manager) Names(only string) ([]string, error) {
	if only != "" {
		if _, err := m.member(only); err != nil {
			return nil, err
		}

		return []string{only}, nil
	}

	names := make([]string, 0, len(m.members))
	for _, mem := range m.members {
		names = append(names, mem.name)
	}

	return names, nil
}

// member returns the provider of this name, or an errdefs.ErrNotFound error.
func (m *Manager) member(name string) (*member, error) {
	if mem, ok := m.byName[name]; ok {
		return mem, nil
	}

	names, _ := m.Names("")

	return nil, errdefs.NotFound("no provider %q in the configuration; it has %s", name, strings.Join(names, ", "))
}

// CountRunnersWith sets how the fleet counts the scale sets' runners on each
// provider, which max_runners is checked against along with the runners being
// made. Until it is called, only those being made count.
func (m *Manager) CountRunnersWith(counter func() map[string]int) {
	m.counterMu.Lock()
	defer m.counterMu.Unlock()

	m.counter = counter
}

// runnerCounts returns the scale sets' runners on each provider, not counting
// those being made.
func (m *Manager) runnerCounts() map[string]int {
	m.counterMu.RLock()
	counter := m.counter
	m.counterMu.RUnlock()

	if counter == nil {
		return map[string]int{}
	}

	return counter()
}

// Probe returns the named providers as placement sees them, in the order
// given. It calls no provider.
func (m *Manager) Probe(names []string) []types.ProviderSnapshot {
	counts := m.runnerCounts()
	states := make([]types.ProviderSnapshot, 0, len(names))

	for _, name := range names {
		mem, err := m.member(name)
		if err != nil {
			states = append(states, types.ProviderSnapshot{Name: name, Err: "no such provider"})
			continue
		}

		states = append(states, mem.snapshot(counts[name]))
	}

	return states
}

// SetDisabled takes a provider out of placement, or puts it back, until the
// daemon restarts. Runners already on it are left alone.
func (m *Manager) SetDisabled(name string, disabled bool) error {
	mem, err := m.member(name)
	if err != nil {
		return err
	}

	mem.setDisabled(disabled)

	verb := "enabled"
	if disabled {
		verb = "disabled"
	}
	m.logger.Info("provider "+verb+" until the daemon restarts", slog.String("provider", name))

	return nil
}

// Close closes every provider.
func (m *Manager) Close() error {
	var errs []error
	for _, mem := range m.members {
		if err := mem.provider.Close(); err != nil {
			errs = append(errs, fmt.Errorf("provider %q: %w", mem.name, err))
		}
	}

	return errors.Join(errs...)
}
