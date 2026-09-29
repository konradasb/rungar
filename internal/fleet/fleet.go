// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package fleet places runners on providers. It tries a scale set's providers
// in order until one creates the runner, and remembers each refusal: a scale
// set skips a provider that refused its runner for a while, and one that said
// it was full is kept for the scale set against lower priorities. It never
// asks a provider whether it has room.
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

// ProviderConfig is one configured, connected provider.
type ProviderConfig struct {
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

// Config configures a Manager. Zero durations take the defaults.
type Config struct {
	// BackoffFirst and BackoffMax bound how long a scale set skips a
	// provider that refused its runner.
	BackoffFirst time.Duration
	BackoffMax   time.Duration

	// HoldDown is how long a scale set that found a provider full keeps
	// lower priorities off it.
	HoldDown time.Duration

	// Events records providers found full or failing to create a runner. Nil
	// records nothing.
	Events EventRecorder

	// Calls records every call made to a provider. Nil records nothing.
	Calls ProviderCallRecorder

	// Logger is where the fleet logs. Nil discards the logs.
	Logger *slog.Logger
}

// EventRecorder records events; *events.Log is one. Record must not block.
type EventRecorder interface {
	Record(e events.Event)
}

var _ EventRecorder = (*events.Log)(nil)

// discardEvents is an EventRecorder that records nothing.
type discardEvents struct{}

func (discardEvents) Record(events.Event) {}

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
	events  EventRecorder
	logger  *slog.Logger

	// mu guards counter.
	mu      sync.RWMutex
	counter func() map[string]int
}

// New returns a fleet of the providers, in the order given.
func New(providers []ProviderConfig, cfg Config) *Manager {
	cfg = withDefaults(cfg)

	m := &Manager{byName: make(map[string]*member, len(providers)), events: cfg.Events, logger: cfg.Logger}
	for _, p := range providers {
		each := newMember(p, cfg)
		m.members = append(m.members, each)
		m.byName[p.Name] = each
	}

	return m
}

// withDefaults fills in the settings left unset.
func withDefaults(cfg Config) Config {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Events == nil {
		cfg.Events = discardEvents{}
	}
	if cfg.BackoffFirst <= 0 {
		cfg.BackoffFirst = defaultBackoffFirst
	}
	if cfg.BackoffMax <= 0 {
		cfg.BackoffMax = defaultBackoffMax
	}
	if cfg.HoldDown <= 0 {
		cfg.HoldDown = defaultHoldDown
	}

	return cfg
}

// Names returns every provider's name, in the fleet's order.
func (m *Manager) Names() []string {
	names := make([]string, 0, len(m.members))
	for _, each := range m.members {
		names = append(names, each.name)
	}

	return names
}

// CheckName returns an errdefs.ErrNotFound error naming the providers there
// are if the fleet has none of this name.
func (m *Manager) CheckName(name string) error {
	_, err := m.member(name)
	return err
}

// member returns the provider of this name, or an errdefs.ErrNotFound error
// naming those there are.
func (m *Manager) member(name string) (*member, error) {
	if found, ok := m.byName[name]; ok {
		return found, nil
	}

	return nil, errdefs.NotFound("no provider %q in the configuration; it has %s", name,
		strings.Join(m.Names(), ", "))
}

// SetRunnerCounter sets how the fleet counts the scale sets' runners on each
// provider, which max_runners is checked against along with the runners being
// created. Until it is called, only those being created count.
func (m *Manager) SetRunnerCounter(counter func() map[string]int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.counter = counter
}

// runnerCounts returns the scale sets' runners on each provider, not counting
// those being created.
func (m *Manager) runnerCounts() map[string]int {
	m.mu.RLock()
	counter := m.counter
	m.mu.RUnlock()

	if counter == nil {
		return map[string]int{}
	}

	return counter()
}

// Snapshots returns the named providers as placement sees them, in the order
// given. It calls no provider.
func (m *Manager) Snapshots(names []string) []types.ProviderSnapshot {
	counts := m.runnerCounts()
	snapshots := make([]types.ProviderSnapshot, 0, len(names))

	for _, name := range names {
		found, err := m.member(name)
		if err != nil {
			snapshots = append(snapshots, types.ProviderSnapshot{Name: name, Error: "no such provider"})
			continue
		}

		snapshots = append(snapshots, found.snapshot(counts[name]))
	}

	return snapshots
}

// SetDisabled takes a provider out of placement, or puts it back, until the
// daemon restarts. Runners already on it are left alone.
func (m *Manager) SetDisabled(name string, disabled bool) error {
	found, err := m.member(name)
	if err != nil {
		return err
	}

	found.setDisabled(disabled)
	m.logger.Info("provider placement changed until the daemon restarts",
		slog.String("provider", name), slog.Bool("disabled", disabled))

	return nil
}

// Close closes every provider.
func (m *Manager) Close() error {
	var errs []error
	for _, each := range m.members {
		if err := each.provider.Close(); err != nil {
			errs = append(errs, fmt.Errorf("provider %q: %w", each.name, err))
		}
	}

	return errors.Join(errs...)
}
