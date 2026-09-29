// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"time"

	ghscaleset "github.com/actions/scaleset"
	"golang.org/x/sync/errgroup"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/events"
	"github.com/konradasb/rungar/internal/fleet"
	"github.com/konradasb/rungar/internal/github"
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
	Runners(ctx context.Context) ([]github.Runner, error)
}

// gitHubStatusSource says whether runners are online and busy, which the scale
// set API does not.
type gitHubStatusSource interface {
	GitHubStatus(ctx context.Context, name string) (types.GitHubStatus, error)

	// OfflineRunners returns the names of the runners GitHub has disconnected.
	OfflineRunners(ctx context.Context) ([]string, error)

	// RefreshIfStale lists GitHub's runners unless the cached listing is
	// fresh, returning GitHub's error if it does not answer.
	RefreshIfStale(ctx context.Context) error
}

// Fleet is the providers the scale sets place their runners on.
type Fleet interface {
	// Names returns every provider's name, in configuration order.
	Names() []string

	// CheckName returns an errdefs.ErrNotFound error if no provider has this
	// name.
	CheckName(name string) error

	// ScaleSetProviders returns one scale set's providers, in its order.
	ScaleSetProviders(spec types.ScaleSetSpec) *fleet.ScaleSetProviders

	// Snapshots returns the named providers as placement sees them. It
	// calls no provider.
	Snapshots(names []string) []types.ProviderSnapshot

	// Providers returns the named providers, with the machines listed on
	// them and how each stands for the scale sets. It calls no provider.
	Providers(names []string, sets []types.ScaleSetSpec, machines []types.Machine) []types.Provider

	// List returns the machines carrying the selector's labels on the named
	// providers. Providers that cannot be listed make it return a
	// *fleet.UnreachableError, with the machines of the others.
	List(ctx context.Context, names []string, selector map[string]string) ([]types.Machine, error)

	// Delete deletes a machine. One already gone is not an error.
	Delete(ctx context.Context, provider, machine string) error

	// SetDisabled takes a provider out of placement, or puts it back.
	SetDisabled(name string, disabled bool) error

	// SetRunnerCounter sets how the scale sets' runners on each provider are
	// counted, against its max_runners.
	SetRunnerCounter(counter func() map[string]int)
}

// EventRecorder records what happens to runners and scale sets. Its method
// must not block.
type EventRecorder interface {
	Record(e events.Event)
}

// MetricsRecorder records what the scale sets do. Its methods must not block.
type MetricsRecorder interface {
	SetPaused(scaleSet string, paused bool)
	SetMinRunners(scaleSet string, n int)
	SetDesiredRunners(scaleSet string, n int)
	CountRunnerCreated(scaleSet, provider string)
	CountRunnerRemoved(scaleSet, provider string, reason types.RemovalReason)
	CountRunnerLost(scaleSet, provider string, reason types.LossReason)
	CountScaleUpFailed(scaleSet string, reason types.CallResult)
	CountJobStarted(scaleSet string)
	CountJobCompleted(scaleSet, result string)
	SetJobsAssigned(scaleSet string, n int)
	ObserveJobWait(scaleSet string, total, forRunner time.Duration)
	ObserveRunnerCreateDuration(scaleSet, provider string, d time.Duration)
	ObserveRunnerBootDuration(scaleSet, provider string, d time.Duration)
	SetLastPoll(scaleSet string, at time.Time)
	ObserveReconcile(scaleSet string, finished time.Time, d time.Duration, listed bool)
	SetRunners(scaleSet string, runners []types.Runner)
	SetProviderReachability(states []types.ProviderSnapshot)
}

var (
	_ API                = (*ghscaleset.Client)(nil)
	_ EventRecorder      = (*events.Log)(nil)
	_ GitHubRunnerLister = (*github.Client)(nil)
	_ gitHubStatusSource = (*gitHubRunnerCache)(nil)
	_ Fleet              = (*fleet.Manager)(nil)
	_ scaleSetProviders  = (*fleet.ScaleSetProviders)(nil)
)

// Config holds a Manager's dependencies. Fleet, GitHub and GitHubRunners are
// required, and NewScaleSetClient is too when there are scale sets.
type Config struct {
	// ScaleSets are the configured scale sets, in configuration order.
	ScaleSets []types.ScaleSetSpec

	// Installation labels every runner. ReconcileInterval is how often each
	// scale set is reconciled with the fleet.
	Installation      string
	ReconcileInterval time.Duration

	// Fleet is the providers the scale sets place their runners on.
	Fleet Fleet

	// GitHub answers for scale sets not configured. NewScaleSetClient creates
	// each configured scale set a client of its own, which carries its ID.
	// GitHubRunners says whether runners are online.
	GitHub            API
	NewScaleSetClient func() (*ghscaleset.Client, error)
	GitHubRunners     GitHubRunnerLister

	// Events records runners created, adopted, removed and lost. Nil records
	// nothing.
	Events EventRecorder

	// Metrics records what the scale sets do. Nil records nothing.
	Metrics MetricsRecorder

	// Logger logs what the scale sets do. Nil logs nothing.
	Logger *slog.Logger

	// ListenerLogger logs what the scale sets' listeners do, which is
	// written a level lower: INFO records at DEBUG. Nil logs nothing.
	ListenerLogger *slog.Logger
}

// Manager runs every configured scale set and answers for them and their
// runners. Its methods are safe for concurrent use, before and during Run.
type Manager struct {
	installation   string
	specs          []types.ScaleSetSpec
	interval       time.Duration
	fleet          Fleet
	github         API
	gitHubStatuses gitHubStatusSource
	metrics        MetricsRecorder
	events         EventRecorder
	logger         *slog.Logger

	// listenerLogger is the scale sets' listeners' logger, which writes
	// INFO records at DEBUG.
	listenerLogger *slog.Logger

	// sets are the configured scale sets, in configuration order.
	sets []*scaleSet
}

// New returns a Manager of the configured scale sets. It contacts nothing:
// GitHub and the providers are first asked by Run. It returns an error if a
// required field of cfg is nil.
func New(cfg Config) (*Manager, error) {
	switch {
	case cfg.Fleet == nil:
		return nil, errors.New("scale sets need a fleet; set Config.Fleet")
	case cfg.GitHub == nil:
		return nil, errors.New("scale sets need a GitHub client; set Config.GitHub")
	case cfg.GitHubRunners == nil:
		return nil, errors.New("scale sets need a lister of GitHub's runners; set Config.GitHubRunners")
	case len(cfg.ScaleSets) > 0 && cfg.NewScaleSetClient == nil:
		return nil, errors.New("scale sets need their clients; set Config.NewScaleSetClient")
	}

	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.ListenerLogger == nil {
		cfg.ListenerLogger = slog.New(slog.DiscardHandler)
	}
	if cfg.Metrics == nil {
		cfg.Metrics = discardMetrics{}
	}
	if cfg.Events == nil {
		cfg.Events = discardEvents{}
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
		listenerLogger: demoted(cfg.ListenerLogger),
	}

	for _, spec := range cfg.ScaleSets {
		client, err := cfg.NewScaleSetClient()
		if err != nil {
			return nil, err
		}

		m.sets = append(m.sets, m.newConfiguredScaleSet(spec, client))
	}

	if len(m.sets) > 0 {
		l := newLease(m.sets[0].spec.Name)
		for _, s := range m.sets {
			s.lease = l
		}
		m.sets[0].isLead = true
	}

	m.fleet.SetRunnerCounter(m.machinesByProvider)

	return m, nil
}

// Run runs every scale set until ctx is cancelled or one of them fails.
func (m *Manager) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)

	for _, s := range m.sets {
		g.Go(func() error { return s.run(ctx) })
	}

	g.Go(func() error {
		m.warnLeftovers(ctx)
		return nil
	})

	g.Go(func() error {
		m.recordProviderMetrics(ctx)
		return nil
	})

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	m.logger.Info("stopped; the runners are left for the next start to adopt")

	return nil
}

// warnLeftovers warns of each scale set no longer configured whose runners are
// still on the fleet, where nothing looks after them. It looks once: a scale
// set is left over by a change of configuration, which the daemon takes up
// only by restarting. Providers that cannot be listed are not waited for.
func (m *Manager) warnLeftovers(ctx context.Context) {
	machines, err := m.listAllMachines(ctx, "")
	if _, err := unreachableOf(err); err != nil {
		m.logger.Debug("cannot look for scale sets no longer configured", slog.Any("error", err))
		return
	}

	counts := runnersByScaleSet(machines)
	for _, s := range m.sets {
		delete(counts, s.spec.Name)
	}

	for _, name := range slices.Sorted(maps.Keys(counts)) {
		m.logger.Warn("scale set no longer configured still has runners on the fleet, and nothing looks after "+
			"them; remove it with rungar scale-sets rm, unless another Rungar of this installation serves it",
			slog.String("scale_set", name), slog.Int("runners", counts[name]))
	}
}

// Reconcile reconciles the named scale sets now, or every one if names is
// empty, and returns them afterwards. It stops at the first that does not hold
// its message session, with checkHeld's errdefs.ErrUnavailable error, having
// reconciled those before it.
func (m *Manager) Reconcile(ctx context.Context, names []string) ([]types.ScaleSet, error) {
	sets := m.sets
	if len(names) > 0 {
		sets = make([]*scaleSet, 0, len(names))
		for _, name := range names {
			s, ok := m.configuredScaleSet(name)
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

// recordProviderMetrics records the providers' metrics every reconcile
// interval until ctx is cancelled.
func (m *Manager) recordProviderMetrics(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		m.metrics.SetProviderReachability(m.fleet.Snapshots(m.fleet.Names()))
	}
}

// machinesByProvider counts every configured scale set's machines not yet
// deleted on each provider, which the providers' max_runners limits: a runner
// whose machine cannot be deleted keeps its place until it is.
func (m *Manager) machinesByProvider() map[string]int {
	out := map[string]int{}

	for _, s := range m.sets {
		for provider, n := range s.machinesByProvider() {
			out[provider] += n
		}
	}

	return out
}

// configuredScaleSet returns the configured scale set of this name, and
// whether there is one.
func (m *Manager) configuredScaleSet(name string) (*scaleSet, bool) {
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
	return m.listMachines(ctx, scaleSet, m.fleet.Names())
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
