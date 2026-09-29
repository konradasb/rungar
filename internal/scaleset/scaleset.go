// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ghscaleset "github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
	"golang.org/x/sync/errgroup"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/events"
	"github.com/konradasb/rungar/internal/types"
)

// fleetGroup is one scale set's providers.
type fleetGroup interface {
	// List returns the machines carrying the selector's labels. Providers
	// that cannot be listed make it return a *fleet.UnreachableError, with
	// the machines of the others.
	List(ctx context.Context, selector map[string]string) ([]types.Machine, error)

	// CanPlace returns an errdefs.ErrNoCapacity error if Place would try no
	// provider. It asks the providers nothing.
	CanPlace() error

	// Place makes one runner's machine, from spec, on the first provider
	// that takes it, and returns that provider. runners counts the scale
	// set's runners on each provider. The runner counts against its
	// provider's limit until release is called. It returns an
	// errdefs.ErrNoCapacity error when no provider takes the runner, with
	// the provider if one failed and left a machine it could not remove.
	Place(ctx context.Context, runners map[string]int,
		spec func(provider string) types.MachineSpec) (provider string, release func(), err error)

	// HoldBackReason says why the scale set is holding back for a higher
	// priority, or is empty.
	HoldBackReason() string

	// Delete removes a machine. One already gone is not an error.
	Delete(ctx context.Context, provider, name string) error
}

// scaleSet is one configured scale set. What it knows of its runners is a
// cache of the fleet, built by adopt and kept current by reconcileFleet.
type scaleSet struct {
	spec  types.ScaleSetSpec
	fleet fleetGroup

	// github and client are the same client: client for what only the
	// concrete type does, carrying the scale set's ID and opening sessions.
	github API
	client *ghscaleset.Client

	gitHubStatuses gitHubStatusSource
	installation   string
	interval       time.Duration
	metrics        metricsRecorder
	events         events.Recorder
	logger         *slog.Logger
	listenerLog    *slog.Logger

	// id is the scale set's ID on GitHub, set once run has found or made it.
	id atomic.Int64

	// adopted is set once the runners already on the fleet are known.
	adopted atomic.Bool

	// wake has the scale set reconciled now rather than at its next tick.
	wake chan struct{}

	// mu guards the fields below. It is never held across a call to a
	// provider or GitHub.
	mu      sync.Mutex
	phase   types.ScaleSetPhase
	runners map[string]*types.Runner

	// paused keeps the scale set from taking jobs; see setPaused. listener
	// is the running listener, whose capacity follows it, or nil.
	paused   bool
	listener *listener.Listener

	// unreachable is when each provider that could not be listed was first
	// found so.
	unreachable map[string]time.Time

	// offline is when each runner was first found disconnected from GitHub.
	offline map[string]time.Time

	// creating are the runners whose machines are being made, which
	// reconcileFleet must not adopt.
	creating map[string]bool

	// removed is when each recently removed runner was removed, so that
	// reconcileFleet does not adopt its machine while it is being deleted.
	removed map[string]time.Time

	// decide serialises scaling decisions. assigned is the job count of the
	// last, and assignedKnown whether there has been one.
	decide        sync.Mutex
	assigned      int
	assignedKnown bool

	// desired is the target of the last decision, or -1 before the first. It
	// is apart from decide, which a decision holds while it makes runners.
	desired atomic.Int64

	// pass serialises reconcile passes.
	pass sync.Mutex
}

var _ listener.Scaler = (*scaleSet)(nil)

func (m *Manager) newScaleSet(spec types.ScaleSetSpec, client *ghscaleset.Client) *scaleSet {
	s := &scaleSet{
		spec:           spec,
		fleet:          m.fleet.Group(spec),
		github:         client,
		client:         client,
		gitHubStatuses: m.gitHubStatuses,
		installation:   m.installation,
		interval:       m.interval,
		metrics:        m.metrics,
		events:         m.events,
		logger:         component(m.logger, "scaleset").With(slog.String("scale_set", spec.Name)),
		listenerLog:    demoted(component(m.logger, "listener").With(slog.String("scale_set", spec.Name))),
		wake:           make(chan struct{}, 1),
		phase:          types.ScaleSetStarting,
		runners:        map[string]*types.Runner{},
		paused:         spec.Paused,
		unreachable:    map[string]time.Time{},
		offline:        map[string]time.Time{},
		creating:       map[string]bool{},
		removed:        map[string]time.Time{},
	}
	s.desired.Store(-1)
	m.metrics.SetPaused(spec.Name, spec.Paused)

	return s
}

// run runs the scale set until ctx is cancelled or its listener stops,
// leaving its runners where they are.
func (s *scaleSet) run(ctx context.Context) error {
	id, err := s.ensureOnGitHub(ctx)
	if err != nil {
		return err
	}

	s.id.Store(int64(id))
	s.client.SetSystemInfo(systemInfo(id))

	if err := s.adopt(ctx); err != nil {
		return err
	}

	session, err := s.openSession(ctx, id)
	if err != nil {
		return err
	}
	defer s.closeSession(ctx, session)

	s.setPhase(types.ScaleSetListening)

	l, err := listener.New(session, listener.Config{
		ScaleSetID: id,
		MaxRunners: s.spec.MaxRunners,
		Logger:     s.listenerLog,
	})
	if err != nil {
		return fmt.Errorf("scale set %q: create listener: %w", s.spec.Name, err)
	}
	s.setListener(l)
	defer s.setListener(nil)

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		s.logger.Info("listening for jobs",
			slog.Any("labels", s.spec.AllLabels()),
			slog.Any("providers", s.spec.ProviderNames()),
			slog.String("placement", string(s.spec.Placement)),
			slog.Int("max_runners", s.spec.MaxRunners),
			slog.Int("priority", s.spec.Priority),
			slog.Bool("paused", s.isPaused()))

		if err := l.Run(ctx, s); err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("scale set %q: listener: %w", s.spec.Name, err)
		}

		return nil
	})

	g.Go(func() error {
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			case <-s.wake:
			}

			_ = s.reconcile(ctx)
		}
	})

	return g.Wait()
}

// reconcile reconciles the scale set with the fleet, retries its last scaling
// decision and records its runner metrics. It returns an
// errdefs.ErrUnavailable error until the scale set has adopted its runners.
func (s *scaleSet) reconcile(ctx context.Context) error {
	if !s.adopted.Load() {
		return errdefs.Unavailable("scale set %q is still starting; try again", s.spec.Name)
	}

	s.pass.Lock()
	defer s.pass.Unlock()

	if err := s.reconcileFleet(ctx); err != nil && !errors.Is(err, context.Canceled) {
		s.logger.Debug("cannot reconcile against the fleet", slog.Any("error", err))
	}

	s.retry(ctx)
	s.metrics.SetRunners(s.spec.Name, s.list())

	return nil
}

// status returns the scale set as it runs, without what GitHub or the
// providers have of it.
func (s *scaleSet) status() types.ScaleSet {
	set := types.ScaleSet{
		Spec: s.spec,
		Status: types.ScaleSetStatus{
			Configured:        true,
			Phase:             s.currentPhase(),
			Paused:            s.isPaused(),
			HoldingBackReason: s.fleet.HoldBackReason(),
		},
	}

	if s.adopted.Load() {
		set.Status.Runners = s.list()
	}
	set.Status.Desired, set.Status.DesiredKnown = s.desiredCount()

	return set
}

func (s *scaleSet) setPhase(p types.ScaleSetPhase) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.phase = p
}

func (s *scaleSet) currentPhase() types.ScaleSetPhase {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.phase
}

// count returns how many runners the scale set has.
func (s *scaleSet) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.runners)
}

// list returns the scale set's runners, by name.
func (s *scaleSet) list() []types.Runner {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]types.Runner, 0, len(s.runners))
	for _, r := range s.runners {
		out = append(out, *r)
	}
	slices.SortFunc(out, func(a, b types.Runner) int { return strings.Compare(a.Name, b.Name) })

	return out
}

// runnersPerProvider counts the scale set's runners on each provider.
func (s *scaleSet) runnersPerProvider() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()

	counts := map[string]int{}
	for _, r := range s.runners {
		counts[r.Provider]++
	}

	return counts
}

// runnerSelector returns the labels every machine of the scale set carries.
func (s *scaleSet) runnerSelector() map[string]string {
	return types.ScaleSetSelector(s.installation, s.spec.Name)
}
