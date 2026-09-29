// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"log/slog"
	"time"

	ghscaleset "github.com/actions/scaleset"
	"golang.org/x/sync/errgroup"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/events"
	"github.com/konradasb/rungar/internal/fleet"
	"github.com/konradasb/rungar/internal/github"
	"github.com/konradasb/rungar/internal/metrics"
	"github.com/konradasb/rungar/internal/types"
)

// API is the part of GitHub's scale set API the scale sets use.
type API interface {
	GetRunnerGroupByName(ctx context.Context, name string) (*ghscaleset.RunnerGroup, error)
	GetRunnerScaleSet(ctx context.Context, groupID int, name string) (*ghscaleset.RunnerScaleSet, error)
	CreateRunnerScaleSet(ctx context.Context, set *ghscaleset.RunnerScaleSet) (*ghscaleset.RunnerScaleSet, error)
	DeleteRunnerScaleSet(ctx context.Context, id int) error
	GenerateJitRunnerConfig(ctx context.Context,
		setting *ghscaleset.RunnerScaleSetJitRunnerSetting, scaleSetID int,
	) (*ghscaleset.RunnerScaleSetJitRunnerConfig, error)
	GetRunnerByName(ctx context.Context, name string) (*ghscaleset.RunnerReference, error)
	RemoveRunner(ctx context.Context, runnerID int64) error
}

// GitHubRunnerLister lists every self-hosted runner GitHub has in the scope.
type GitHubRunnerLister interface {
	ListRunners(ctx context.Context) ([]github.Runner, error)
}

// gitHubStatusSource says whether runners are online and busy, which the scale
// set API does not.
type gitHubStatusSource interface {
	GitHubStatus(ctx context.Context, name string) (types.GitHubStatus, error)

	// RefreshIfStale lists GitHub's runners unless the cached listing is
	// fresh, returning GitHub's error if it does not answer.
	RefreshIfStale(ctx context.Context) error
}

// metricsRecorder records what the scale sets do. Its methods must not block.
type metricsRecorder interface {
	SetPaused(scaleSet string, paused bool)
	SetDesiredRunners(scaleSet string, n int)
	CountRunnerCreated(scaleSet, provider string)
	CountRunnerRemoved(scaleSet, provider string, reason types.RemovalReason)
	CountScaleUpFailed(scaleSet, reason string)
	CountJobStarted(scaleSet string)
	CountJobCompleted(scaleSet, result string)
	SetRunners(scaleSet string, runners []types.Runner)
	SetProviders(states []types.ProviderSnapshot)
}

var (
	_ API                = (*ghscaleset.Client)(nil)
	_ GitHubRunnerLister = (*github.Client)(nil)
	_ gitHubStatusSource = (*gitHubRunnerCache)(nil)
	_ metricsRecorder    = (*metrics.Metrics)(nil)
	_ fleetGroup         = (*fleet.Group)(nil)
)

// Config holds a Manager's dependencies.
type Config struct {
	ScaleSets []types.ScaleSetSpec

	// Installation labels every runner. ReconcileInterval is how often each
	// scale set is reconciled with the fleet.
	Installation      string
	ReconcileInterval time.Duration

	Fleet *fleet.Manager

	// GitHub answers for scale sets not configured. NewScaleSetClient makes
	// each configured scale set a client of its own, which carries its ID.
	// GitHubRunners says whether runners are online.
	GitHub            API
	NewScaleSetClient func() (*ghscaleset.Client, error)
	GitHubRunners     GitHubRunnerLister

	// Events records runners made, adopted, removed and lost. Nil records
	// nothing.
	Events events.Recorder

	Metrics *metrics.Metrics
	Logger  *slog.Logger
}

// Manager runs every configured scale set and answers for them and their
// runners. Its methods are safe for concurrent use, before and during Run.
type Manager struct {
	installation   string
	specs          []types.ScaleSetSpec
	interval       time.Duration
	fleet          *fleet.Manager
	github         API
	gitHubStatuses gitHubStatusSource
	metrics        metricsRecorder
	events         events.Recorder
	logger         *slog.Logger

	// sets are the configured scale sets, in configuration order.
	sets []*scaleSet
}

// New returns a Manager of the configured scale sets. It contacts nothing:
// GitHub and the providers are first asked by Run.
func New(cfg Config) (*Manager, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Metrics == nil {
		cfg.Metrics = metrics.New()
	}
	if cfg.Events == nil {
		cfg.Events = events.Discard
	}

	m := &Manager{
		installation:   cfg.Installation,
		specs:          cfg.ScaleSets,
		interval:       cfg.ReconcileInterval,
		fleet:          cfg.Fleet,
		github:         cfg.GitHub,
		gitHubStatuses: newGitHubRunnerCache(cfg.GitHubRunners),
		metrics:        cfg.Metrics,
		events:         cfg.Events,
		logger:         cfg.Logger,
	}

	for _, spec := range cfg.ScaleSets {
		client, err := cfg.NewScaleSetClient()
		if err != nil {
			return nil, err
		}

		m.sets = append(m.sets, m.newScaleSet(spec, client))
	}

	if m.fleet != nil {
		m.fleet.CountRunnersWith(m.runnersByProvider)
	}

	return m, nil
}

// Run runs every scale set until ctx is cancelled or one of them fails.
func (m *Manager) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)

	for _, s := range m.sets {
		g.Go(func() error { return s.run(ctx) })
	}

	g.Go(func() error {
		m.observeFleet(ctx)
		return nil
	})

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	m.logger.Info("stopped; the runners are left for the next start to adopt")

	return nil
}

// Reconcile reconciles the named scale sets now, or every one if names is
// empty, and returns them afterwards.
func (m *Manager) Reconcile(ctx context.Context, names []string) ([]types.ScaleSet, error) {
	sets := m.sets
	if len(names) > 0 {
		sets = make([]*scaleSet, 0, len(names))
		for _, name := range names {
			s, ok := m.configured(name)
			if !ok {
				return nil, errdefs.NotFound("no scale set %q in the configuration", name)
			}
			sets = append(sets, s)
		}
	}

	out := make([]types.ScaleSet, 0, len(sets))
	for _, s := range sets {
		if err := s.reconcile(ctx); err != nil {
			return nil, err
		}
		out = append(out, s.status())
	}

	return out, nil
}

// observeFleet records the providers' metrics every reconcile interval until
// ctx is cancelled.
func (m *Manager) observeFleet(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		names, _ := m.fleet.Names("")
		m.metrics.SetProviders(m.fleet.Probe(names))
	}
}

// runnersByProvider counts every configured scale set's runners on each
// provider, which the providers' max_runners limits.
func (m *Manager) runnersByProvider() map[string]int {
	out := map[string]int{}

	for _, s := range m.sets {
		for provider, n := range s.runnersPerProvider() {
			out[provider] += n
		}
	}

	return out
}

// configured returns the configured scale set of this name.
func (m *Manager) configured(name string) (*scaleSet, bool) {
	for _, s := range m.sets {
		if s.spec.Name == name {
			return s, true
		}
	}

	return nil, false
}

// listMachines lists the installation's runners on the named providers, of one
// scale set, or of all if scaleSet is empty. Providers that cannot be listed
// make it return a *fleet.UnreachableError.
func (m *Manager) listMachines(ctx context.Context, scaleSet string, providers []string) ([]types.Machine, error) {
	return m.fleet.List(ctx, providers, m.runnerSelector(scaleSet))
}

// listAllMachines is listMachines on every provider.
func (m *Manager) listAllMachines(ctx context.Context, scaleSet string) ([]types.Machine, error) {
	names, _ := m.fleet.Names("")
	return m.listMachines(ctx, scaleSet, names)
}

// runnerSelector returns the labels of the installation's runners, of one
// scale set, or of all if scaleSet is empty.
func (m *Manager) runnerSelector(scaleSet string) map[string]string {
	selector := types.ScaleSetSelector(m.installation, scaleSet)
	if scaleSet == "" {
		delete(selector, types.LabelScaleSet)
	}

	return selector
}

// unreachableOf splits a listing's error into the providers that could not be
// listed and any other error.
func unreachableOf(err error) (map[string]error, error) {
	var unreachable *fleet.UnreachableError
	if errors.As(err, &unreachable) {
		return unreachable.Providers, nil
	}

	return nil, err
}
