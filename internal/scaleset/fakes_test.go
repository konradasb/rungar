// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	ghscaleset "github.com/actions/scaleset"
	"github.com/google/uuid"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/events"
	"github.com/konradasb/rungar/internal/fleet"
	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

const (
	gib = 1 << 30

	testScaleSet     = "rungar-vm"
	testInstallation = "rungar-test"
)

// fakeScaleSetProviders is a scale set's providers, in memory. It records
// what is asked of it and places as the real ones do, trying the providers in
// order and moving on from one that refuses. It models every provider, rather
// than scripting each call, so that a test can say "these providers, this one
// full".
type fakeScaleSetProviders struct {
	mu sync.Mutex

	// providers are the fake providers, in the scale set's order. One with too
	// little spare refuses a runner as full.
	providers []*providerRoom

	// machines are each provider's machines, and held what each machine the
	// fleet created takes from its provider.
	machines map[string][]types.Machine
	held     map[string]types.Resources

	// Recorded calls.
	created []createdMachine
	deleted []deletedMachine

	// Injected failures. List reports unreachable providers as a real
	// fleet does. createErr fails every create, canPlaceErr is what
	// CanPlace returns, and failLeavingMachine has Place fail on the first
	// provider, leaving a machine behind.
	unreachable        map[string]bool
	listErr            error
	createErr          error
	deleteErr          error
	canPlaceErr        error
	failLeavingMachine error

	// createDelay is how long a create takes. createGate, when set, lists a
	// machine from the start of its create and holds the create there,
	// deleteGate holds a Delete before it deletes, and listGate a List
	// before it lists; each holds one call.
	createDelay time.Duration
	createGate  *gate
	deleteGate  *gate
	listGate    *gate
}

type createdMachine struct {
	provider string
	spec     types.MachineSpec
}

type deletedMachine struct {
	provider, name string
}

// providerRoom is one provider and what it has spare, in vCPUs and whole GiB.
type providerRoom struct {
	name      string
	vcpus     int
	memoryGiB int64
}

// newFakeScaleSetProviders returns a scale set's providers of the given ones.
func newFakeScaleSetProviders(providers ...providerRoom) *fakeScaleSetProviders {
	f := &fakeScaleSetProviders{machines: map[string][]types.Machine{}, held: map[string]types.Resources{}}

	for _, p := range providers {
		f.providers = append(f.providers, &p)
	}

	return f
}

// setRoom sets what a provider has spare.
func (f *fakeScaleSetProviders) setRoom(name string, vcpus int, memoryGiB int64) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, p := range f.providers {
		if p.name == name {
			p.vcpus, p.memoryGiB = vcpus, memoryGiB
		}
	}
}

// CanPlace returns canPlaceErr if set, and no capacity when the fleet has no
// provider.
func (f *fakeScaleSetProviders) CanPlace() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.canPlaceErr != nil {
		return f.canPlaceErr
	}
	if len(f.providers) == 0 {
		return errdefs.NoCapacity("no provider can be tried")
	}

	return nil
}

// Place tries the providers as the fleet does for spread, fewest runners
// first, moving on from one that refuses and deleting what a failed one may
// have left.
func (f *fakeScaleSetProviders) Place(ctx context.Context, runners map[string]int,
	spec func(provider string) types.MachineSpec,
) (string, func(), error) {
	f.mu.Lock()
	failure := f.failLeavingMachine
	var order []string
	for _, p := range f.providers {
		order = append(order, p.name)
	}
	f.mu.Unlock()

	if failure != nil && len(order) > 0 {
		machine := spec(order[0])
		f.mu.Lock()
		f.machines[order[0]] = append(f.machines[order[0]], types.Machine{
			Name: machine.Name, Labels: machine.Labels, State: types.MachineStopped, CreatedAt: time.Now(),
		})
		f.mu.Unlock()

		return order[0], nil, failure
	}

	slices.SortStableFunc(order, func(a, b string) int { return runners[a] - runners[b] })

	var refusals []string
	for _, name := range order {
		machine := spec(name)

		err := f.create(name, machine)
		if err == nil {
			return name, func() {}, nil
		}

		refusals = append(refusals, name+": "+err.Error())
		if !errors.Is(err, errdefs.ErrNoCapacity) {
			_ = f.Delete(context.WithoutCancel(ctx), name, machine.Name)
		}
	}

	return "", nil, errdefs.NoCapacity("no provider took the runner: %s", strings.Join(refusals, "; "))
}

// HoldingBackReason says the scale set never holds back.
func (f *fakeScaleSetProviders) HoldingBackReason() string { return "" }

func (f *fakeScaleSetProviders) List(_ context.Context, selector map[string]string) ([]types.Machine, error) {
	f.mu.Lock()
	gate := f.listGate
	f.mu.Unlock()

	gate.pass()

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.listErr != nil {
		return nil, f.listErr
	}

	var out []types.Machine
	partial := &fleet.UnreachableError{Providers: map[string]error{}}
	for providerName, machines := range f.machines {
		if f.unreachable[providerName] {
			partial.Providers[providerName] = errors.New("connection refused")
			continue
		}
		for _, m := range machines {
			if types.HasLabels(m.Labels, selector) {
				m.Provider = providerName
				out = append(out, m)
			}
		}
	}

	if len(partial.Providers) > 0 {
		return out, partial
	}

	return out, nil
}

// create creates a machine on a provider, refusing as full a runner it has too
// little spare for.
func (f *fakeScaleSetProviders) create(providerName string, spec types.MachineSpec) error {
	f.mu.Lock()
	gate := f.createGate
	f.mu.Unlock()

	if gate == nil {
		// Outside the lock, so that concurrent creates overlap.
		time.Sleep(max(f.createDelay, time.Millisecond))
	}

	f.mu.Lock()
	if f.createErr != nil {
		f.mu.Unlock()
		return f.createErr
	}

	want := types.Resources{}
	if r, ok := spec.Runner.(runnerSize); ok {
		want = types.Resources(r)
	}

	p := f.provider(providerName)
	if want.VCPUs > p.vcpus || want.Memory.Bytes() > p.memoryGiB*gib {
		f.mu.Unlock()
		return errdefs.NoCapacity("not enough room for %s", want)
	}
	p.vcpus -= want.VCPUs
	p.memoryGiB -= want.Memory.Bytes() / gib
	f.held[spec.Name] = want

	f.created = append(f.created, createdMachine{provider: providerName, spec: spec})
	f.machines[providerName] = append(f.machines[providerName], types.Machine{
		Name:      spec.Name,
		Labels:    spec.Labels,
		State:     types.MachineRunning,
		Size:      spec.Runner.Describe(),
		CreatedAt: time.Now(),
	})
	f.mu.Unlock()

	// A provider may list a machine before the call creating it returns.
	gate.pass()

	return nil
}

// provider returns the provider of this name. f.mu must be held.
func (f *fakeScaleSetProviders) provider(name string) *providerRoom {
	for _, p := range f.providers {
		if p.name == name {
			return p
		}
	}

	return &providerRoom{name: name}
}

func (f *fakeScaleSetProviders) Delete(_ context.Context, providerName, name string) error {
	f.mu.Lock()
	gate := f.deleteGate
	f.mu.Unlock()

	// Outside the lock, so that the fleet can be listed meanwhile.
	gate.pass()

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.deleteErr != nil {
		return f.deleteErr
	}

	f.deleted = append(f.deleted, deletedMachine{provider: providerName, name: name})

	kept := f.machines[providerName][:0]
	for _, machine := range f.machines[providerName] {
		if machine.Name != name {
			kept = append(kept, machine)
			continue
		}
		if r, ok := f.held[name]; ok {
			p := f.provider(providerName)
			p.vcpus += r.VCPUs
			p.memoryGiB += r.Memory.Bytes() / gib
			delete(f.held, name)
		}
	}
	f.machines[providerName] = kept

	return nil
}

// gate holds a call of the fake fleet until the test releases it: reached is
// closed when the first call reaches it, and the test closes released to let
// every call held go on.
type gate struct {
	reached, released chan struct{}
	reachedOnce       sync.Once
}

func newGate() *gate {
	return &gate{reached: make(chan struct{}), released: make(chan struct{})}
}

// pass marks the gate reached and waits for it to be released. A nil gate is
// passed at once.
func (g *gate) pass() {
	if g == nil {
		return
	}

	g.reachedOnce.Do(func() { close(g.reached) })
	<-g.released
}

// put adds a machine to a provider as if Rungar had created it.
func (f *fakeScaleSetProviders) put(providerName, name string, state types.MachineState, created time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.machines[providerName] = append(f.machines[providerName], types.Machine{
		Name:      name,
		Labels:    types.RunnerLabels(testInstallation, testScaleSet, name, ""),
		State:     state,
		Size:      "2 vCPU, 4 GiB",
		CreatedAt: created,
	})
}

// providerOf returns the provider a machine is on, or "" if there is none.
func (f *fakeScaleSetProviders) providerOf(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()

	for providerName, machines := range f.machines {
		for _, machine := range machines {
			if machine.Name == name {
				return providerName
			}
		}
	}

	return ""
}

// machine returns the machine of this name as the fleet lists it, failing the
// test if there is none.
func (f *fakeScaleSetProviders) machine(t *testing.T, name string) types.Machine {
	t.Helper()

	f.mu.Lock()
	defer f.mu.Unlock()

	for providerName, machines := range f.machines {
		for _, machine := range machines {
			if machine.Name == name {
				machine.Provider = providerName
				return machine
			}
		}
	}

	t.Fatalf("no machine %q on the fleet", name)

	return types.Machine{}
}

// count returns how many machines the fleet holds.
func (f *fakeScaleSetProviders) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	var n int
	for _, machines := range f.machines {
		n += len(machines)
	}

	return n
}

// runnerSize is a runner spec that is only a size, which the fake providers
// read to refuse what does not fit.
type runnerSize types.Resources

func (r runnerSize) Resources() types.Resources { return types.Resources(r) }
func (r runnerSize) Describe() string           { return types.Resources(r).String() }

// testScaleSetSpec returns the tests' scale set: on every provider of the
// fleet, with one size of runner.
func testScaleSetSpec(f *fakeScaleSetProviders) types.ScaleSetSpec {
	set := types.ScaleSetSpec{
		Name:         testScaleSet,
		MaxRunners:   10,
		StartTimeout: time.Minute,
		RunnerGroup:  types.DefaultRunnerGroup,
		RunnerSpecs:  map[string]types.RunnerSpec{},
	}
	for _, p := range f.providers {
		set.Providers = append(set.Providers, types.ProviderRef{Name: p.name})
		set.RunnerSpecs[p.name] = runnerSize{VCPUs: 2, Memory: 4 * gib}
	}

	return set
}

// newTestScaleSet returns a scale set over the fleet, with GitHub faked. It has
// not adopted its runners yet.
func newTestScaleSet(f *fakeScaleSetProviders, opts ...func(*fakeGitHub)) *scaleSet {
	gh := &fakeGitHub{}
	for _, opt := range opts {
		opt(gh)
	}

	return newScaleSetOver(f, gh, testScaleSetSpec(f), &fakeMetrics{}, discardLogger())
}

// newScaleSetOver returns a scale set of spec over the fleet and GitHub, as the
// Manager creates one, with its GitHub ID set.
func newScaleSetOver(f scaleSetProviders, gh *fakeGitHub, spec types.ScaleSetSpec, metrics MetricsRecorder,
	logger *slog.Logger,
) *scaleSet {
	s := newScaleSet(spec)
	s.providers = f
	s.github = gh
	s.gitHubStatuses = gh
	s.installation = testInstallation
	s.interval = time.Minute
	s.metrics = metrics
	s.events = &fakeEvents{}
	s.logger = logger
	s.listenerLogger = logger
	s.openMessageSession = (&fakeSessions{}).open
	s.sessionRetry = time.Millisecond
	s.id.Store(1)

	return s
}

// discardLogger returns a logger that writes nothing.
func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// fakeGitHub is GitHub's scale set API and runner statuses, answering as a test
// says. By default it registers every runner, cannot be asked for a status,
// and removes registrations.
type fakeGitHub struct {
	// mu guards the fields its methods change: busy, removed, runnerIDs,
	// sets, created and deletedSetIDs. The rest are set by the test while
	// no call is under way.
	mu sync.Mutex

	// jit, status and removeRunner answer for runners when set.
	jit          func(ctx context.Context, name string) (string, error)
	status       statusFunc
	removeRunner func(ctx context.Context, name string) error

	// busy are runners GitHub has given a job, whose registrations it
	// refuses to remove; removed records those it removed.
	busy    map[string]bool
	removed []string

	// runnerIDs are the runners asked about; a runner's ID is its index.
	runnerIDs []string

	// registrations maps each runner GitHub has to its scale set ID; nil
	// has every runner, in the tests' scale set. offline are the runners
	// listed disconnected.
	registrations map[string]int
	offline       []string

	// sets are GitHub's scale sets by name, and groups its runner groups;
	// nil groups has every group, with ID 1.
	sets   map[string]*ghscaleset.RunnerScaleSet
	groups map[string]int

	// created records what CreateRunnerScaleSet was asked for, and
	// deletedSetIDs the IDs DeleteRunnerScaleSet was given.
	created       *ghscaleset.RunnerScaleSet
	deletedSetIDs []int

	groupErr, getErr, createErr error
}

// RefreshIfStale fails unless the test set status.
func (g *fakeGitHub) RefreshIfStale(context.Context) error {
	if g.status == nil {
		return errors.New("GitHub is not asked in this test")
	}

	return nil
}

// statusFunc answers for a runner's status.
type statusFunc func(ctx context.Context, name string) (answer, error)

// answer is a runner's status, as a test writes it.
type answer struct {
	Registered, Online, Busy bool
}

// registered returns a statusFunc answering from runners; a runner not in it is
// not registered.
func registered(runners map[string]answer) statusFunc {
	return func(_ context.Context, name string) (answer, error) {
		return runners[name], nil
	}
}

func (g *fakeGitHub) GitHubStatus(ctx context.Context, name string) (types.GitHubStatus, error) {
	if g.status == nil {
		return "", errors.New("GitHub is not asked in this test")
	}

	a, err := g.status(ctx, name)

	switch {
	case err != nil:
		return "", err
	case !a.Registered:
		return types.GitHubNotRegistered, nil
	case a.Busy:
		return types.GitHubBusy, nil
	case a.Online:
		return types.GitHubIdle, nil
	default:
		return types.GitHubOffline, nil
	}
}

func (g *fakeGitHub) GenerateJitRunnerConfig(
	ctx context.Context, setting *ghscaleset.RunnerScaleSetJitRunnerSetting, _ int,
) (*ghscaleset.RunnerScaleSetJitRunnerConfig, error) {
	config := "jit-" + setting.Name
	if g.jit != nil {
		var err error
		if config, err = g.jit(ctx, setting.Name); err != nil {
			return nil, err
		}
	}

	return &ghscaleset.RunnerScaleSetJitRunnerConfig{EncodedJITConfig: config}, nil
}

// GetRunnerByName has the registered runners, with IDs in the order asked for.
func (g *fakeGitHub) GetRunnerByName(_ context.Context, name string) (*ghscaleset.RunnerReference, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	scaleSetID := 1
	if g.registrations != nil {
		var ok bool
		if scaleSetID, ok = g.registrations[name]; !ok {
			return nil, nil //nolint:nilnil // as GitHub's client answers for a runner it does not have
		}
	}

	id := slices.Index(g.runnerIDs, name)
	if id < 0 {
		g.runnerIDs = append(g.runnerIDs, name)
		id = len(g.runnerIDs) - 1
	}

	return &ghscaleset.RunnerReference{ID: id, Name: name, RunnerScaleSetID: scaleSetID}, nil
}

// OfflineRunners returns offline.
func (g *fakeGitHub) OfflineRunners(context.Context) ([]string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	return slices.Clone(g.offline), nil
}

func (g *fakeGitHub) RemoveRunner(ctx context.Context, id int64) error {
	g.mu.Lock()
	name := g.runnerIDs[id]
	busy := g.busy[name]
	g.mu.Unlock()

	switch {
	case g.removeRunner != nil:
		return g.removeRunner(ctx, name)
	case busy:
		return ghscaleset.JobStillRunningError
	}

	g.mu.Lock()
	g.removed = append(g.removed, name)
	g.mu.Unlock()

	return nil
}

func (g *fakeGitHub) setBusy(name string, busy bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.busy == nil {
		g.busy = map[string]bool{}
	}
	g.busy[name] = busy
}

func (g *fakeGitHub) GetRunnerGroupByName(_ context.Context, name string) (*ghscaleset.RunnerGroup, error) {
	if g.groupErr != nil {
		return nil, g.groupErr
	}
	if g.groups == nil {
		return &ghscaleset.RunnerGroup{ID: 1, Name: name}, nil
	}

	id, ok := g.groups[name]
	if !ok {
		return nil, errors.New("no runner group " + name)
	}

	return &ghscaleset.RunnerGroup{ID: id, Name: name}, nil
}

func (g *fakeGitHub) GetRunnerScaleSet(_ context.Context, _ int, name string) (*ghscaleset.RunnerScaleSet, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.getErr != nil {
		return nil, g.getErr
	}

	return g.sets[name], nil
}

func (g *fakeGitHub) CreateRunnerScaleSet(
	_ context.Context, set *ghscaleset.RunnerScaleSet,
) (*ghscaleset.RunnerScaleSet, error) {
	if g.createErr != nil {
		return nil, g.createErr
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	g.created = set

	out := *set
	out.ID = 42

	return &out, nil
}

func (g *fakeGitHub) DeleteRunnerScaleSet(_ context.Context, id int) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.deletedSetIDs = append(g.deletedSetIDs, id)
	for name, set := range g.sets {
		if set.ID == id {
			delete(g.sets, name)
		}
	}

	return nil
}

// fakeMetrics is a MetricsRecorder that keeps what it is told.
type fakeMetrics struct {
	mu              sync.Mutex
	paused          []bool
	desired         []int
	minRunners      []int
	created         []string
	removed         []types.RemovalReason
	lost            []types.LossReason
	scaleUpFailures []types.CallResult
	jobsStarted     int
	jobResults      []string
	assigned        []int
	jobWaits        []jobWait
	createDurations []time.Duration
	bootDurations   []time.Duration
	polls           int
	reconciles      []bool
}

func (r *fakeMetrics) SetPaused(_ string, paused bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paused = append(r.paused, paused)
}

func (r *fakeMetrics) SetMinRunners(_ string, n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.minRunners = append(r.minRunners, n)
}

func (r *fakeMetrics) SetDesiredRunners(_ string, n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.desired = append(r.desired, n)
}

func (r *fakeMetrics) CountRunnerCreated(_, provider string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.created = append(r.created, provider)
}

func (r *fakeMetrics) CountRunnerRemoved(_, _ string, reason types.RemovalReason) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removed = append(r.removed, reason)
}

func (r *fakeMetrics) CountRunnerLost(_, _ string, reason types.LossReason) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lost = append(r.lost, reason)
}

func (r *fakeMetrics) CountScaleUpFailed(_ string, reason types.CallResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.scaleUpFailures = append(r.scaleUpFailures, reason)
}

func (r *fakeMetrics) CountJobStarted(string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobsStarted++
}

func (r *fakeMetrics) CountJobCompleted(_, result string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobResults = append(r.jobResults, result)
}

func (r *fakeMetrics) SetJobsAssigned(_ string, n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.assigned = append(r.assigned, n)
}

func (r *fakeMetrics) ObserveJobWait(_ string, total, forRunner time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobWaits = append(r.jobWaits, jobWait{total: total, forRunner: forRunner})
}

func (r *fakeMetrics) ObserveRunnerCreateDuration(_, _ string, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.createDurations = append(r.createDurations, d)
}

func (r *fakeMetrics) ObserveRunnerBootDuration(_, _ string, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bootDurations = append(r.bootDurations, d)
}

func (r *fakeMetrics) SetLastPoll(string, time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.polls++
}

func (r *fakeMetrics) ObserveReconcile(_ string, _ time.Time, _ time.Duration, listed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reconciles = append(r.reconciles, listed)
}

func (r *fakeMetrics) SetRunners(string, []types.Runner)                {}
func (r *fakeMetrics) SetProviderReachability([]types.ProviderSnapshot) {}

// removals returns the reasons of the removals s counted, in order.
func removals(t *testing.T, s *scaleSet) []types.RemovalReason {
	t.Helper()

	r, ok := s.metrics.(*fakeMetrics)
	if !ok {
		t.Fatalf("metrics = %T, want the recorder", s.metrics)
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.removed)
}

// managerRemovals returns the reasons of the removals m counted itself, in
// order.
func managerRemovals(t *testing.T, m *Manager) []types.RemovalReason {
	t.Helper()

	r, ok := m.metrics.(*fakeMetrics)
	if !ok {
		t.Fatalf("metrics = %T, want the recorder", m.metrics)
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.removed)
}

// losses returns the reasons of the losses s counted, in order.
func losses(t *testing.T, s *scaleSet) []types.LossReason {
	t.Helper()

	r, ok := s.metrics.(*fakeMetrics)
	if !ok {
		t.Fatalf("metrics = %T, want the recorder", s.metrics)
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.lost)
}

// fakeEvents is an EventRecorder that keeps what it is told.
type fakeEvents struct {
	mu     sync.Mutex
	events []events.Event
}

func (e *fakeEvents) Record(event events.Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, event)
}

// recorded returns the events recorded, in order.
func (e *fakeEvents) recorded() []events.Event {
	e.mu.Lock()
	defer e.mu.Unlock()

	return slices.Clone(e.events)
}

// recordedBy returns the events rec, a *fakeEvents, recorded, in order.
func recordedBy(t *testing.T, rec EventRecorder) []events.Event {
	t.Helper()

	e, ok := rec.(*fakeEvents)
	if !ok {
		t.Fatalf("events = %T, want the fake", rec)
	}

	return e.recorded()
}

// fakeSessions is GitHub's message session on the scale set, of which it gives
// one at a time.
type fakeSessions struct {
	mu sync.Mutex

	// other is set while another daemon holds the session, and current is
	// the one held by the scale set, or nil. unanswered is how many more
	// attempts to open one get no answer.
	other      bool
	current    *fakeSession
	opened     int
	unanswered int
}

func (f *fakeSessions) open(context.Context, int, string) (messageSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.unanswered > 0 {
		f.unanswered--
		return nil, unansweredError()
	}
	if f.other || f.current != nil {
		return nil, errors.New("409 Conflict: GitHub.Actions.Runtime.WebApi.RunnerScaleSetSessionConflictException")
	}

	f.current = &fakeSession{owner: f, lost: make(chan error, 1)}
	f.opened++

	return f.current, nil
}

// setOther has another daemon hold the session, or end it. One taken from the
// scale set fails its next poll.
func (f *fakeSessions) setOther(held bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.other = held
	if held && f.current != nil {
		f.current.lost <- errors.New("404 Not Found: the session was deleted")
		f.current = nil
	}
}

// setUnanswered has the next n attempts to open a session get no answer.
func (f *fakeSessions) setUnanswered(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.unanswered = n
}

// held reports whether the scale set holds the session.
func (f *fakeSessions) held() bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.current != nil
}

func (f *fakeSessions) openedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.opened
}

// unansweredError is the scale set client's error for a request GitHub never
// answered.
func unansweredError() error {
	const u = "https://broker.actions.githubusercontent.com/rest/_apis/runtime/runnerscalesets/3/sessions"

	return fmt.Errorf("failed to create message session: failed to do the session request: failed to issue "+
		"the request: request POST %s failed: failed to send request: %w", u,
		&url.Error{Op: "Post", URL: u, Err: errors.New("dial tcp 20.85.130.105:443: connect: connection refused")})
}

// fakeSession is a held message session on which GitHub sends no messages.
type fakeSession struct {
	owner *fakeSessions
	lost  chan error
}

func (s *fakeSession) GetMessage(ctx context.Context, _, _ int) (*ghscaleset.RunnerScaleSetMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-s.lost:
		return nil, err
	}
}

func (s *fakeSession) DeleteMessage(context.Context, int) error { return nil }

func (s *fakeSession) AcquireJobs(context.Context, []int64) ([]int64, error) { return nil, nil }

func (s *fakeSession) Session() ghscaleset.RunnerScaleSetSession {
	return ghscaleset.RunnerScaleSetSession{SessionID: uuid.New(), Statistics: &ghscaleset.RunnerScaleSetStatistic{}}
}

func (s *fakeSession) Close(context.Context) error {
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()

	if s.owner.current == s {
		s.owner.current = nil
	}

	return nil
}

// waitFor waits for cond to hold, failing the test if it does not soon.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()

	for deadline := time.Now().Add(5 * time.Second); !cond(); {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// registrationRemovals records which runners' registrations were removed.
type registrationRemovals struct {
	mu    sync.Mutex
	names []string
}

func (d *registrationRemovals) remove(_ context.Context, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.names = append(d.names, name)

	return nil
}

func (d *registrationRemovals) got() []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	return slices.Clone(d.names)
}

func removingRegistrationsWith(d *registrationRemovals) func(*fakeGitHub) {
	return func(o *fakeGitHub) { o.removeRunner = d.remove }
}

// removedRegistrations returns the registrations GitHub was asked to remove.
func removedRegistrations(gh *fakeGitHub) []string {
	gh.mu.Lock()
	defer gh.mu.Unlock()

	return slices.Clone(gh.removed)
}

// statusWith returns an option having GitHub answer for runners with fn.
func statusWith(fn statusFunc) func(*fakeGitHub) {
	return func(o *fakeGitHub) { o.status = fn }
}

// runningAJob has GitHub say every runner is running a job, so that scaling
// leaves it.
var runningAJob = statusWith(func(context.Context, string) (answer, error) {
	return answer{Registered: true, Online: true, Busy: true}, nil
})

// says is a statusFunc with a fixed answer.
func says(registered, online bool) statusFunc {
	return func(context.Context, string) (answer, error) {
		return answer{Registered: registered, Online: online}, nil
	}
}

// happened is an event as a test checks it: what happened, to which runner,
// and why.
type happened struct {
	action events.Action
	name   string
	reason string
}

// happenings returns what events say happened, in order.
func happenings(recorded []events.Event) []happened {
	out := make([]happened, 0, len(recorded))
	for _, e := range recorded {
		out = append(out, happened{action: e.Action, name: e.Name, reason: e.Attributes["reason"]})
	}

	return out
}

// newScaling returns a scale set of the given bounds on a fleet with room for
// fits runners, and the metrics it reports to. It serves, as though it held
// its session.
func newScaling(t *testing.T, fits, minRunners, maxRunners int, opts ...func(*fakeGitHub),
) (*scaleSet, *fakeScaleSetProviders, *fakeMetrics) {
	t.Helper()

	f := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 2 * fits, memoryGiB: int64(4 * fits)})
	gh := &fakeGitHub{}
	for _, opt := range opts {
		opt(gh)
	}

	spec := testScaleSetSpec(f)
	spec.MinRunners, spec.MaxRunners = minRunners, maxRunners

	rec := &fakeMetrics{}
	s := newScaleSetOver(f, gh, spec, rec, discardLogger())
	s.serving.Store(true)

	return s, f, rec
}

// markAllBusy has every runner start a job.
func markAllBusy(s *scaleSet) {
	for _, r := range s.sortedRunners() {
		s.markBusy(r.Name, types.Job{ID: "job"})
	}
}

// fakeProvider is a provider held in memory: the machines on it.
type fakeProvider struct {
	mu sync.Mutex

	machines []types.Machine

	// refuse is what Create answers with, if anything.
	refuse error

	// deleted records what Delete was asked to delete, and deleteErr is what
	// it answers with, if anything.
	deleted   []string
	deleteErr error

	// createGate, when set, holds a Create once its machine is listed.
	createGate *gate
}

var _ provider.Provider = (*fakeProvider)(nil)

func (p *fakeProvider) List(_ context.Context, selector map[string]string) ([]types.Machine, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var out []types.Machine
	for _, m := range p.machines {
		if types.HasLabels(m.Labels, selector) {
			out = append(out, m)
		}
	}

	return out, nil
}

// Create creates the machine, unless the test has it refuse.
func (p *fakeProvider) Create(_ context.Context, spec types.MachineSpec) error {
	p.mu.Lock()
	if p.refuse != nil {
		p.mu.Unlock()
		return p.refuse
	}
	p.machines = append(p.machines, types.Machine{
		Name: spec.Name, Labels: spec.Labels, State: types.MachineRunning, CreatedAt: time.Now(),
	})
	gate := p.createGate
	p.mu.Unlock()

	// Outside the lock, so that the provider can be listed meanwhile.
	gate.pass()

	return nil
}

func (p *fakeProvider) Close() error { return nil }

// Delete deletes the machine, unless the test has it fail.
func (p *fakeProvider) Delete(_ context.Context, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.deleted = append(p.deleted, name)
	if p.deleteErr != nil {
		return p.deleteErr
	}
	p.machines = slices.DeleteFunc(p.machines, func(m types.Machine) bool { return m.Name == name })

	return nil
}

// fixture is a Manager that has not been run, without a message session:
// scale set rungar-vm on compute1, a fleet holding the runners given, and
// GitHub holding rungar-vm and gone-vm.
type fixture struct {
	m       *Manager
	github  *fakeGitHub
	compute *fakeProvider

	// onGitHub is what GitHub says of each runner.
	onGitHub map[string]answer
}

// newFixture returns a fixture over runners written scaleSet/name.
func newFixture(t *testing.T, runners ...string) *fixture {
	t.Helper()

	compute := &fakeProvider{}

	f := &fixture{compute: compute, onGitHub: map[string]answer{}}
	f.github = &fakeGitHub{
		status: registered(f.onGitHub),
		sets: map[string]*ghscaleset.RunnerScaleSet{
			"rungar-vm": {ID: 7, Name: "rungar-vm", RunnerGroupName: "Default",
				Statistics: &ghscaleset.RunnerScaleSetStatistic{TotalAssignedJobs: 3, TotalRunningJobs: 2}},
			"gone-vm": {ID: 9, Name: "gone-vm", RunnerGroupName: "Default"},
		},
	}

	for _, r := range runners {
		scaleSet, name, _ := strings.Cut(r, "/")
		compute.machines = append(compute.machines, types.Machine{
			Name:      name,
			Labels:    types.RunnerLabels(testInstallation, scaleSet, name, ""),
			State:     types.MachineRunning,
			CreatedAt: time.Now().Add(-time.Minute),
		})
		f.onGitHub[name] = answer{Registered: true, Online: true}
	}

	fleetManager := fleet.New([]fleet.ProviderConfig{{Name: "compute1", Type: "test", Weight: 1, Backend: compute}},
		fleet.Config{})

	spec := types.ScaleSetSpec{
		Name: "rungar-vm", MaxRunners: 4, StartTimeout: time.Minute, RunnerGroup: types.DefaultRunnerGroup,
		Providers:   []types.ProviderRef{{Name: "compute1"}},
		RunnerSpecs: map[string]types.RunnerSpec{"compute1": runnerSize{VCPUs: 2, Memory: 4 * gib}},
	}

	f.m = &Manager{
		installation:   testInstallation,
		specs:          []types.ScaleSetSpec{spec},
		interval:       time.Minute,
		fleet:          fleetManager,
		github:         f.github,
		gitHubStatuses: f.github,
		metrics:        &fakeMetrics{},
		events:         &fakeEvents{},
		logger:         discardLogger(),
	}
	f.m.sets = []*scaleSet{
		newScaleSetOver(fleetManager.ScaleSetProviders(spec), f.github, spec, &fakeMetrics{}, discardLogger()),
	}

	return f
}

// adopt has rungar-vm adopt its runners, as it does once running.
func (f *fixture) adopt(t *testing.T) *scaleSet {
	t.Helper()

	s := f.m.sets[0]
	if err := s.adopt(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.setPhase(types.ScaleSetListening)

	return s
}
