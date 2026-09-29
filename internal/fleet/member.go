// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package fleet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

// member is one provider of the fleet, and what placement has learnt of it by
// using it.
type member struct {
	name               string
	typ                string
	weight             float64
	endpoint           string
	configuredDisabled bool
	maxRunners         int
	provider           provider.Provider
	logger             *slog.Logger

	backoffFirst time.Duration
	backoffMax   time.Duration
	holdDown     time.Duration

	now func() time.Time // time.Now, replaced in tests

	// mu guards the fields below. It is never held across a call to the
	// provider.
	mu       sync.Mutex
	disabled bool

	// creating counts runners being created here, which no scale set counts
	// yet.
	creating int

	// reachable is whether the last List succeeded, and listErr why not.
	// reachableKnown is whether any List has been made.
	reachable, reachableKnown bool
	listErr                   error

	// backoffs holds, by scale set, the backoff after the provider refused
	// that scale set's runners.
	backoffs map[string]*backoff

	// hold is the highest priority scale set waiting for room here, if any.
	hold hold
}

// backoff is how long a scale set leaves a provider alone after the provider
// refused its runners.
type backoff struct {
	refusals int       // refusals in a row
	until    time.Time // the provider is skipped until then
	full     bool      // whether the last refusal said the provider was full
	refusal  string    // the last refusal's error
}

// active reports whether the provider is still skipped at now.
func (b *backoff) active(now time.Time) bool {
	return b != nil && now.Before(b.until)
}

// hold is a scale set waiting for room on a provider, which keeps lower
// priorities off it for the hold-down.
type hold struct {
	scaleSet string
	priority int
	since    time.Time
}

// maxDoublings caps backoff doublings, so that the duration cannot overflow.
const maxDoublings = 20

func newMember(p ProviderConfig, cfg Config) *member {
	backend := p.Backend
	if cfg.Calls != nil {
		backend = measuredProvider{Provider: backend, name: p.Name, recorder: cfg.Calls}
	}

	return &member{
		name:               p.Name,
		typ:                p.Type,
		weight:             p.Weight,
		endpoint:           p.Endpoint,
		configuredDisabled: p.Disabled,
		maxRunners:         p.MaxRunners,
		provider:           backend,
		logger:             cfg.Logger.With(slog.String("provider", p.Name)),
		backoffFirst:       cfg.BackoffFirst,
		backoffMax:         cfg.BackoffMax,
		holdDown:           cfg.HoldDown,
		now:                time.Now,
		disabled:           p.Disabled,
		backoffs:           map[string]*backoff{},
	}
}

// snapshot returns the provider as placement sees it, with count of the scale
// sets' runners on it.
func (m *member) snapshot(count int) types.ProviderSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	s := types.ProviderSnapshot{
		Name:        m.name,
		Type:        m.typ,
		Weight:      m.weight,
		Disabled:    m.disabled,
		Reachable:   m.reachable || !m.reachableKnown,
		MaxRunners:  m.maxRunners,
		RunnerCount: count + m.creating,
	}
	if !s.Reachable && m.listErr != nil {
		s.Error = m.listErr.Error()
	}
	if m.holdActive() {
		s.Hold = &types.Hold{
			ScaleSet:  m.hold.scaleSet,
			Priority:  m.hold.priority,
			Remaining: m.holdDown - m.now().Sub(m.hold.since),
		}
	}

	return s
}

// describe returns the provider with its snapshot, and how it stands for each
// of the scale sets placed on it.
func (m *member) describe(snapshot types.ProviderSnapshot, sets []types.ScaleSetSpec) types.Provider {
	p := types.Provider{Snapshot: snapshot, Endpoint: m.endpoint, ConfiguredDisabled: m.configuredDisabled}

	for _, set := range sets {
		if !slices.Contains(set.ProviderNames(), m.name) {
			continue
		}

		p.ScaleSets = append(p.ScaleSets, m.providerScaleSet(set.Name))
	}

	return p
}

// providerScaleSet returns how the provider stands for a scale set: how much
// longer the scale set skips it, and why.
func (m *member) providerScaleSet(scaleSet string) types.ProviderScaleSet {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := types.ProviderScaleSet{Name: scaleSet}

	now := m.now()
	if b := m.backoffs[scaleSet]; b.active(now) {
		state.BackoffFor = b.until.Sub(now)
		state.Refusals = b.refusals
		state.Refusal = b.refusal
		state.Full = b.full
	}

	return state
}

func (m *member) setDisabled(disabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.disabled = disabled
}

// recordListResult records whether the provider is reachable from what a
// List returned, logging when that changes. A call its caller cancelled says
// nothing of the provider.
func (m *member) recordListResult(err error) {
	if errors.Is(err, context.Canceled) {
		return
	}

	m.mu.Lock()
	wasReachable, known := m.reachable, m.reachableKnown
	m.reachable, m.reachableKnown, m.listErr = err == nil, true, err
	m.mu.Unlock()

	switch {
	case err != nil && (wasReachable || !known):
		m.logger.Warn("provider is unreachable; nothing is placed on it until it lists again",
			slog.Any("error", err))
	case err == nil && known && !wasReachable:
		m.logger.Info("provider is reachable again")
	}
}

// claim counts a runner as being created here until release is called, unless
// the provider is at its limit. count returns the scale sets' runners on the
// provider.
//
// count is called under m.mu, so that a runner recorded by another scale set
// while this one was choosing is never missed; at worst it is counted twice.
// The lock order is therefore m.mu, then the Manager's mu and the scale sets'
// locks that count takes (see Manager.runnerCounts). Neither of those may be
// held while calling into a member, or claim can deadlock.
func (m *member) claim(count func() int) (release func(), ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.maxRunners > 0 && count()+m.creating >= m.maxRunners {
		return nil, false
	}
	m.creating++

	var once sync.Once

	return func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()

			m.creating--
		})
	}, true
}

// recordRefusal records that the provider refused a scale set's runner, and
// returns how long the scale set skips it for, how many times in a row it has
// refused, and whether it said it was full. The wait doubles each time in a
// row.
func (m *member) recordRefusal(scaleSet string, err error) (wait time.Duration, refusals int, full bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	b := m.backoffs[scaleSet]
	if b == nil {
		b = &backoff{}
		m.backoffs[scaleSet] = b
	}

	b.refusals++
	b.full = errors.Is(err, errdefs.ErrNoCapacity)
	b.refusal = err.Error()

	wait = doubled(m.backoffFirst, m.backoffMax, b.refusals)
	b.until = m.now().Add(wait)

	return wait, b.refusals, b.full
}

// recordSuccess forgets a scale set's backoff, now the provider has created its
// runner.
func (m *member) recordSuccess(scaleSet string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.backoffs, scaleSet)
}

// skipReason returns why placement does not try the provider for a scale set
// of this priority with this many runners on it, or "" if it tries it.
func (m *member) skipReason(scaleSet string, priority, runners int) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	b := m.backoffs[scaleSet]

	switch {
	case m.disabled:
		return "disabled"
	case m.reachableKnown && !m.reachable:
		return "unreachable"
	case b.active(now) && b.full:
		return "full"
	case b.active(now):
		return "failing to create its runner: " + b.refusal
	case m.holdActive() && priority < m.hold.priority:
		return "held for scale set " + m.hold.scaleSet
	case m.maxRunners > 0 && runners+m.creating >= m.maxRunners:
		return fmt.Sprintf("at its limit of %d runners", m.maxRunners)
	}

	return ""
}

// isFull reports whether a scale set is skipping the provider as full.
func (m *member) isFull(scaleSet string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	b := m.backoffs[scaleSet]

	return b.active(m.now()) && b.full
}

// heldFor returns the higher priority scale set that one of this priority
// holds back from the provider for, if any.
func (m *member) heldFor(priority int) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.holdActive() || priority >= m.hold.priority {
		return "", false
	}

	return m.hold.scaleSet, true
}

// placeHold keeps priorities below this one off the provider for the
// hold-down, for a scale set waiting for room on it, renewing the hold-down if
// the scale set holds it already. A lower priority does not displace a higher
// one's hold.
func (m *member) placeHold(priority int, scaleSet string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.holdActive() && priority < m.hold.priority {
		return
	}

	m.hold = hold{scaleSet: scaleSet, priority: priority, since: m.now()}
}

// releaseHold lifts the hold if a scale set of at least its priority placed a
// runner here.
func (m *member) releaseHold(priority int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.hold.scaleSet != "" && priority >= m.hold.priority {
		m.hold = hold{}
	}
}

// holdActive reports whether a hold is in force. m.mu must be held.
func (m *member) holdActive() bool {
	return m.hold.scaleSet != "" && m.now().Sub(m.hold.since) <= m.holdDown
}

// deleteLeftover deletes the machine a failed create may have left, even if
// the caller has given up.
func (m *member) deleteLeftover(ctx context.Context, machine string) error {
	return m.provider.Delete(context.WithoutCancel(ctx), machine)
}

// doubled returns first doubled once for each time after the first, capped at
// limit.
func doubled(first, limit time.Duration, times int) time.Duration {
	wait := min(first*(1<<min(times-1, maxDoublings)), limit)
	if wait <= 0 {
		wait = limit
	}

	return wait
}
