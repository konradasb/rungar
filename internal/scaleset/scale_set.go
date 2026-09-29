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
	"github.com/konradasb/rungar/internal/types"
)

// scaleSetProviders is one scale set's providers, in its order.
type scaleSetProviders interface {
	// List returns the machines carrying the selector's labels. Providers
	// that cannot be listed make it return a *fleet.UnreachableError, with
	// the machines of the others.
	List(ctx context.Context, selector map[string]string) ([]types.Machine, error)

	// CanPlace returns an errdefs.ErrNoCapacity error if Place would try no
	// provider. It asks the providers nothing.
	CanPlace() error

	// Place creates one runner's machine, from spec, on the first provider
	// that takes it, and returns that provider. runners counts the scale
	// set's runners on each provider. The runner counts against its
	// provider's limit until release is called. It returns an
	// errdefs.ErrNoCapacity error when no provider takes the runner, with
	// the provider if one failed and left a machine it could not delete.
	Place(ctx context.Context, runners map[string]int,
		spec func(provider string) types.MachineSpec) (provider string, release func(), err error)

	// HoldingBackReason says why the scale set is holding back for a higher
	// priority, or is empty.
	HoldingBackReason() string

	// Delete deletes a machine. One already gone is not an error.
	Delete(ctx context.Context, provider, machine string) error
}

// scaleSet is one configured scale set. What it knows of its runners is a
// cache of the fleet, built by adopt and kept current by reconcileFleet.
type scaleSet struct {
	spec      types.ScaleSetSpec
	providers scaleSetProviders

	// github and client are the same client: client for what only the
	// concrete type does, carrying the scale set's ID.
	github API
	client *ghscaleset.Client

	// openMessageSession opens the scale set's message session, and
	// sessionRetry is the first wait before trying again while another
	// holds it.
	openMessageSession sessionOpener
	sessionRetry       time.Duration

	// lease is the daemon's, shared by its scale sets: the lead, which has
	// isLead set, grants it while holding its session, and the others serve
	// only while it is held. Nil serves with no lease.
	lease  *lease
	isLead bool

	gitHubStatuses gitHubStatusSource
	installation   string
	interval       time.Duration
	metrics        MetricsRecorder
	events         EventRecorder
	logger         *slog.Logger
	listenerLogger *slog.Logger

	// now returns the time, which decides the schedule's window in force and
	// times what the scale set does; tests replace it.
	now func() time.Time

	// id is the scale set's ID on GitHub, set once run has found or created it.
	id atomic.Int64

	// serving is set while the scale set holds its message session and knows
	// its runners. Nothing is done to the fleet without it.
	serving atomic.Bool

	// wake has the scale set reconciled now rather than at its next tick.
	wake chan struct{}

	// passLock serialises reconcile passes, removals on request and
	// forgetting the runners when the session is lost, so that none acts on
	// the fleet once the runners are forgotten. decisionLock serialises
	// scaling decisions, and is held while one creates or removes runners.
	// They guard no fields; mu does. They are taken in the order passLock,
	// decisionLock, mu, any of them skipped: a reconcile pass retries the
	// last decision, and a decision takes mu.
	passLock     sync.Mutex
	decisionLock sync.Mutex

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

	// creating are the runners being registered and created, which
	// reconcileFleet must not adopt.
	creating map[string]bool

	// orphans is when each orphaned registration was first found.
	orphans map[string]time.Time

	// leaving are the runners let go of whose machines are still to be
	// deleted, or what became of them still to be recorded; see
	// leavingRunner.
	leaving map[string]*leavingRunner

	// left is when each runner that has left did so, so that
	// reconcileFleet does not adopt its machine from a listing older than
	// its deletion.
	left map[string]time.Time

	// assigned is the job count of the last scaling decision, and
	// assignedKnown whether there has been one. minRunners is the
	// min_runners in force at the last, or -1 before the first.
	assigned      int
	assignedKnown bool
	minRunners    int

	// desired is the target of the last decision, or -1 before the first. It
	// is read without waiting on decisionLock, which a decision holds while it
	// creates runners.
	desired atomic.Int64

	// retryWaiting, if set, is called as retry starts to wait for
	// decisionLock. Tests set it.
	retryWaiting func()
}

var _ listener.Scaler = (*scaleSet)(nil)

// startingRunnerSyncInterval is how often runners still starting are synced
// with GitHub, to see them connect. GitHub's listing is cached for
// gitHubRunnerCacheMaxAge, so a boot duration is measured to within the two
// together.
const startingRunnerSyncInterval = 5 * time.Second

// newScaleSet returns a scale set of spec that knows no runners and has taken
// no scaling decision. Its caller sets what it runs against: its fleet,
// GitHub, metrics, events and loggers.
func newScaleSet(spec types.ScaleSetSpec) *scaleSet {
	s := &scaleSet{
		spec:         spec,
		sessionRetry: sessionRetryFirst,
		now:          time.Now,
		wake:         make(chan struct{}, 1),
		phase:        types.ScaleSetStarting,
		runners:      map[string]*types.Runner{},
		paused:       spec.Paused,
		unreachable:  map[string]time.Time{},
		offline:      map[string]time.Time{},
		creating:     map[string]bool{},
		orphans:      map[string]time.Time{},
		leaving:      map[string]*leavingRunner{},
		left:         map[string]time.Time{},
		minRunners:   -1,
	}
	s.desired.Store(-1)

	return s
}

// newConfiguredScaleSet returns a configured scale set of spec, run against
// the Manager's fleet and GitHub through client.
func (m *Manager) newConfiguredScaleSet(spec types.ScaleSetSpec, client *ghscaleset.Client) *scaleSet {
	scaleSetAttr := slog.String("scale_set", spec.Name)

	s := newScaleSet(spec)
	s.providers = m.fleet.ScaleSetProviders(spec)
	s.github = client
	s.client = client
	s.openMessageSession = sessionOpenerOf(client)
	s.gitHubStatuses = m.gitHubStatuses
	s.installation = m.installation
	s.interval = m.interval
	s.metrics = m.metrics
	s.events = m.events
	s.logger = m.logger.With(scaleSetAttr)
	s.listenerLogger = m.listenerLogger.With(scaleSetAttr)
	m.metrics.SetPaused(spec.Name, spec.Paused)

	return s
}

// run runs the scale set until ctx is cancelled, leaving its runners where
// they are. It acts on the fleet only while it holds the scale set's message
// session, which GitHub gives one daemon at a time, so another daemon of the
// same configuration stands by until it can have it.
func (s *scaleSet) run(ctx context.Context) error {
	id, err := s.ensureOnGitHub(ctx)
	if err != nil {
		return err
	}

	s.id.Store(int64(id))
	s.client.SetSystemInfo(systemInfo(id))

	return s.serve(ctx, id)
}

// serve holds the scale set's message session until ctx is cancelled, and
// then returns ctx's error. A session lost is waited for again, as at start.
// A scale set following the lead holds its own only during the lease's terms,
// and stands by between them.
func (s *scaleSet) serve(ctx context.Context, id int) error {
	for {
		var termEnded <-chan struct{}
		holdCtx, endHold := ctx, func() {}

		if s.isFollower() {
			var err error
			if termEnded, err = s.awaitLead(ctx); err != nil {
				return err
			}

			var cancel context.CancelFunc
			holdCtx, cancel = context.WithCancel(ctx)
			go func() {
				select {
				case <-termEnded:
					cancel()
				case <-holdCtx.Done():
				}
			}()
			endHold = cancel
		}

		err := s.hold(holdCtx, id)
		endHold()

		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case isClosed(termEnded):
			s.logger.Info("the lead scale set's message session was lost; standing by with it, "+
				"leaving the runners to whichever daemon holds it next", slog.String("lead", s.lease.lead))

			continue
		case !errors.Is(err, errSessionLost):
			return err
		}

		s.logger.Warn("lost the message session; leaving the runners to whichever daemon holds it next",
			slog.Duration("retry_in", s.sessionRetry), slog.Any("error", err))

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.sessionRetry):
		}
	}
}

// hold opens the scale set's message session, adopts its runners and takes
// jobs until ctx is cancelled or the session is lost, which it returns as an
// errSessionLost error. The runners are released before the session is closed.
func (s *scaleSet) hold(ctx context.Context, id int) error {
	session, err := s.openSession(ctx, id)
	if err != nil {
		return err
	}
	defer s.closeSession(ctx, session)
	defer s.forgetRunners()

	if s.isLead {
		s.lease.grant()
		defer s.lease.revoke()
	}

	s.setPhase(types.ScaleSetStarting)
	if err := s.adopt(ctx); err != nil {
		return err
	}

	s.setPhase(types.ScaleSetListening)

	l, err := listener.New(session, listener.Config{
		ScaleSetID: id,
		MaxRunners: s.spec.MaxRunners,
		Logger:     s.listenerLogger,
	})
	if err != nil {
		return fmt.Errorf("scale set %q: create listener: %w", s.spec.Name, err)
	}
	s.setListener(l)
	defer s.setListener(nil)

	g, ctx := errgroup.WithContext(ctx)

	// Cleared as soon as the session is let go, not once the goroutines below
	// have stopped, so that no reconcile pass or removal on request starts,
	// and no scaling decision under way goes on creating or removing runners,
	// meanwhile to hold up forgetRunners and closing the session.
	g.Go(func() error {
		<-ctx.Done()
		s.serving.Store(false)

		return nil
	})

	g.Go(func() error {
		s.logger.Info("listening for jobs",
			slog.Any("labels", s.spec.AllLabels()),
			slog.Any("providers", s.spec.ProviderNames()),
			slog.String("placement", string(s.spec.Placement)),
			slog.Int("min_runners", s.spec.MinRunnersAt(s.now())),
			slog.Int("max_runners", s.spec.MaxRunners),
			slog.Int("priority", s.spec.Priority),
			slog.Bool("paused", s.isPaused()))

		// Every failure of the listener is a poll, an acknowledgement or a
		// job acquisition GitHub did not answer over the session.
		if err := l.Run(ctx, s); err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("scale set %q: listener: %w: %w", s.spec.Name, errSessionLost, err)
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

	g.Go(func() error {
		ticker := time.NewTicker(startingRunnerSyncInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}

			s.syncStartingRunners(ctx)
		}
	})

	return g.Wait()
}

// reconcile reconciles the scale set with the fleet, retries its last scaling
// decision and records its runner metrics. It returns checkHeld's error unless
// the scale set holds its session.
func (s *scaleSet) reconcile(ctx context.Context) error {
	s.passLock.Lock()
	defer s.passLock.Unlock()

	// Checked under passLock, so that no pass starts on runners forgotten.
	if err := s.checkHeld(); err != nil {
		return err
	}

	start := s.now()
	err := s.reconcileFleet(ctx)
	finished := s.now()
	s.metrics.ObserveReconcile(s.spec.Name, finished, finished.Sub(start), err == nil)

	if err != nil && !errors.Is(err, context.Canceled) {
		s.logger.Debug("cannot reconcile against the fleet", slog.Any("error", err))
	}

	s.retry(ctx)
	s.metrics.SetRunners(s.spec.Name, s.sortedRunners())

	return nil
}

// isFollower reports whether the scale set serves only while the lead holds its
// session.
func (s *scaleSet) isFollower() bool {
	return s.lease != nil && !s.isLead
}

// awaitLead returns a channel closed when the lease's current term ends,
// standing by until one starts, or ctx's error if it ends first.
func (s *scaleSet) awaitLead(ctx context.Context) (<-chan struct{}, error) {
	s.setPhase(types.ScaleSetWaitingForLead)

	return s.lease.wait(ctx)
}

// isClosed reports whether ch, which may be nil, is closed.
func isClosed(ch <-chan struct{}) bool {
	if ch == nil {
		return false
	}

	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// forgetRunners forgets the scale set's runners and its last scaling decision
// once it no longer holds its session, leaving the runners on the fleet for
// whichever daemon holds it next. A reconcile pass, removal on request or
// scaling decision under way is let finish first, and no pass or removal on
// request starts after. It must not be called from one of them, which holds
// the locks it takes.
func (s *scaleSet) forgetRunners() {
	s.serving.Store(false)
	s.setPhase(types.ScaleSetStarting)

	s.passLock.Lock()
	defer s.passLock.Unlock()
	s.decisionLock.Lock()
	defer s.decisionLock.Unlock()

	s.mu.Lock()
	s.runners = map[string]*types.Runner{}
	clear(s.unreachable)
	clear(s.offline)
	clear(s.orphans)
	s.assigned, s.assignedKnown, s.minRunners = 0, false, -1
	s.desired.Store(-1)
	s.mu.Unlock()

	s.metrics.SetRunners(s.spec.Name, nil)
}

// checkHeld returns an errdefs.ErrUnavailable error unless the scale set holds
// its message session and knows its runners, saying why it does not.
func (s *scaleSet) checkHeld() error {
	if s.serving.Load() {
		return nil
	}

	switch s.currentPhase() {
	case types.ScaleSetWaitingForSession:
		return errdefs.Unavailable("scale set %q is on standby: another daemon holds its message session; "+
			"ask that one", s.spec.Name)
	case types.ScaleSetWaitingForLead:
		return errdefs.Unavailable("scale set %q is on standby until this daemon holds the message session of "+
			"%q, the lead scale set; if another daemon holds it, ask that one", s.spec.Name, s.lease.lead)
	case types.ScaleSetGitHubUnreachable:
		return errdefs.Unavailable("scale set %q has no message session: GitHub does not answer; try again",
			s.spec.Name)
	}

	return errdefs.Unavailable("scale set %q is still starting; try again", s.spec.Name)
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
			HoldingBackReason: s.providers.HoldingBackReason(),
		},
	}

	if s.serving.Load() {
		set.Status.Runners = s.sortedRunners()
	}
	set.Status.Desired, set.Status.DesiredKnown = s.desiredCount()

	now := s.now()
	set.Status.MinRunners = s.spec.MinRunnersAt(now)
	set.Status.Window, set.Status.InWindow = s.spec.Schedule.WindowAt(now)

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

// runnerCount returns how many runners the scale set has.
func (s *scaleSet) runnerCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.runners)
}

// sortedRunners returns copies of the scale set's runners, sorted by name.
func (s *scaleSet) sortedRunners() []types.Runner {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]types.Runner, 0, len(s.runners))
	for _, r := range s.runners {
		out = append(out, *r)
	}
	slices.SortFunc(out, func(a, b types.Runner) int { return strings.Compare(a.Name, b.Name) })

	return out
}

// runnersByProvider counts the scale set's runners on each provider.
func (s *scaleSet) runnersByProvider() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()

	counts := map[string]int{}
	for _, r := range s.runners {
		counts[r.Provider]++
	}

	return counts
}

// machinesByProvider counts the scale set's machines not yet deleted on each
// provider: its runners', and those of the runners leaving it whose machines
// are still to be deleted, which go on taking a place under the provider's
// max_runners until they are. The scale set's own counts are its runners
// alone; see runnersByProvider.
func (s *scaleSet) machinesByProvider() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()

	counts := map[string]int{}
	for _, r := range s.runners {
		counts[r.Provider]++
	}
	for _, l := range s.leaving {
		if !l.deleted {
			counts[l.runner.Provider]++
		}
	}

	return counts
}

// tracks reports whether the scale set has a runner of this name, or is
// creating, letting go of or has just let go of one. s.mu must be held.
func (s *scaleSet) tracks(name string) bool {
	_, known := s.runners[name]
	_, leaving := s.leaving[name]
	_, left := s.left[name]

	return known || leaving || left || s.creating[name]
}

// runnerSelector returns the labels every machine of the scale set carries.
func (s *scaleSet) runnerSelector() map[string]string {
	return types.ScaleSetSelector(s.installation, s.spec.Name)
}
