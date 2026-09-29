// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	ghscaleset "github.com/actions/scaleset"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/events"
	"github.com/konradasb/rungar/internal/fleet"
	"github.com/konradasb/rungar/internal/types"
)

const (
	gib = 1 << 30

	testScaleSet     = "rungar-vm"
	testInstallation = "rungar-test"
)

// fakeFleet is an in-memory fleet. It records what is asked of it and places
// as the real one does, trying the scale set's providers in order and moving
// on from one that refuses. It models the whole fleet, rather than scripting
// each call, so that a test can say "these providers, this one full".
type fakeFleet struct {
	mu sync.Mutex

	// providers are the backends, in the scale set's order. One with too
	// little spare refuses a runner as full.
	providers []*fakeProvider

	// machines are each provider's machines, and held what each machine the
	// fleet made takes from its provider.
	machines map[string][]types.Machine
	held     map[string]types.Resources

	// Recorded calls.
	created []createdMachine
	deleted []deletedMachine

	// Injected failures. List reports unreachable providers as a real
	// fleet does. createErr fails every create, canPlaceErr is what
	// CanPlace returns, and leftover has Place fail on the first provider,
	// leaving a machine behind.
	unreachable map[string]bool
	listErr     error
	createErr   error
	deleteErr   error
	canPlaceErr error
	leftover    error

	// createDelay and deleteDelay are how long a create and a Delete take.
	// listedEarly lists a machine from the start of its create rather than
	// its end.
	createDelay time.Duration
	deleteDelay time.Duration
	listedEarly bool

	// placement is the order Place tries the providers in.
	placement types.Placement

	// holdingBack is what HoldBackReason says.
	holdingBack string
}

type createdMachine struct {
	provider string
	spec     types.MachineSpec
}

type deletedMachine struct {
	provider, name string
}

// fakeProvider is one backend and what it has spare, in vCPUs and whole GiB.
type fakeProvider struct {
	name        string
	vcpus       int
	memoryGiB   int64
	unreachable bool
}

// newFleet returns a fleet of the given providers.
func newFleet(providers ...fakeProvider) *fakeFleet {
	f := &fakeFleet{machines: map[string][]types.Machine{}, held: map[string]types.Resources{}}

	for _, p := range providers {
		f.providers = append(f.providers, &p)
	}

	return f
}

// setRoom sets what a provider has spare.
func (f *fakeFleet) setRoom(name string, vcpus int, memoryGiB int64) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, p := range f.providers {
		if p.name == name {
			p.vcpus, p.memoryGiB = vcpus, memoryGiB
		}
	}
}

// CanPlace returns canPlaceErr if set, and no capacity when every provider is
// unreachable.
func (f *fakeFleet) CanPlace() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.canPlaceErr != nil {
		return f.canPlaceErr
	}
	for _, p := range f.providers {
		if !p.unreachable {
			return nil
		}
	}

	return errdefs.NoCapacity("no provider can be tried")
}

// Place tries the providers as the fleet does: in order for pack, fewest
// runners first for spread, moving on from one that refuses and removing what
// a failed one may have left.
func (f *fakeFleet) Place(ctx context.Context, runners map[string]int,
	spec func(provider string) types.MachineSpec,
) (string, func(), error) {
	f.mu.Lock()
	leftover := f.leftover
	var order []string
	for _, p := range f.providers {
		if !p.unreachable {
			order = append(order, p.name)
		}
	}
	f.mu.Unlock()

	if leftover != nil && len(order) > 0 {
		machine := spec(order[0])
		f.mu.Lock()
		f.machines[order[0]] = append(f.machines[order[0]], types.Machine{
			Name: machine.Name, Labels: machine.Labels, State: types.MachineStopped, CreatedAt: time.Now(),
		})
		f.mu.Unlock()

		return order[0], nil, leftover
	}

	if f.placement != types.PlacementPack {
		slices.SortStableFunc(order, func(a, b string) int { return runners[a] - runners[b] })
	}

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

func (f *fakeFleet) HoldBackReason() string { return f.holdingBack }

func (f *fakeFleet) List(_ context.Context, selector map[string]string) ([]types.Machine, error) {
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
			if types.Matches(m.Labels, selector) {
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

// create makes a machine on a provider, refusing as full a runner it has too
// little spare for.
func (f *fakeFleet) create(providerName string, spec types.MachineSpec) error {
	f.mu.Lock()
	early := f.listedEarly
	f.mu.Unlock()

	if !early {
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
	if want.VCPUs > p.vcpus || want.MemoryBytes > p.memoryGiB*gib {
		f.mu.Unlock()
		return errdefs.NoCapacity("not enough room for %s", want)
	}
	p.vcpus -= want.VCPUs
	p.memoryGiB -= want.MemoryBytes / gib
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

	if early {
		// A provider may list a machine before the call making it returns.
		time.Sleep(f.createDelay)
	}

	return nil
}

// provider returns the backend of this name. f.mu must be held.
func (f *fakeFleet) provider(name string) *fakeProvider {
	for _, p := range f.providers {
		if p.name == name {
			return p
		}
	}

	return &fakeProvider{name: name}
}

func (f *fakeFleet) Delete(_ context.Context, providerName, name string) error {
	// Outside the lock, so that concurrent deletes overlap.
	time.Sleep(f.deleteDelay)

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.deleteErr != nil {
		return f.deleteErr
	}

	f.deleted = append(f.deleted, deletedMachine{provider: providerName, name: name})

	kept := f.machines[providerName][:0]
	for _, inst := range f.machines[providerName] {
		if inst.Name != name {
			kept = append(kept, inst)
			continue
		}
		if r, ok := f.held[name]; ok {
			p := f.provider(providerName)
			p.vcpus += r.VCPUs
			p.memoryGiB += r.MemoryBytes / gib
			delete(f.held, name)
		}
	}
	f.machines[providerName] = kept

	return nil
}

// put adds a machine to a provider as if Rungar had made it.
func (f *fakeFleet) put(providerName, name string, state types.MachineState, created time.Time) {
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
func (f *fakeFleet) providerOf(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()

	for providerName, instances := range f.machines {
		for _, inst := range instances {
			if inst.Name == name {
				return providerName
			}
		}
	}

	return ""
}

// count returns how many machines the fleet holds.
func (f *fakeFleet) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	var n int
	for _, instances := range f.machines {
		n += len(instances)
	}

	return n
}

// runnerSize is a runner spec that is only a size, which the fake backends
// read to refuse what does not fit.
type runnerSize types.Resources

func (r runnerSize) Resources() types.Resources { return types.Resources(r) }
func (r runnerSize) Describe() string           { return types.Resources(r).String() }

// testScaleSetSpec returns the tests' scale set: on every provider of the
// fleet, with one size of runner.
func testScaleSetSpec(f *fakeFleet) types.ScaleSetSpec {
	set := types.ScaleSetSpec{
		Name:         testScaleSet,
		MaxRunners:   10,
		StartTimeout: time.Minute,
		RunnerGroup:  types.DefaultRunnerGroup,
		RunnerSpecs:  map[string]types.RunnerSpec{},
	}
	for _, p := range f.providers {
		set.Providers = append(set.Providers, types.ProviderRef{Name: p.name})
		set.RunnerSpecs[p.name] = runnerSize{VCPUs: 2, MemoryBytes: 4 * gib}
	}

	return set
}

// newTestScaleSet returns a scale set over the fleet, with GitHub faked. It has
// not adopted its runners yet.
func newTestScaleSet(f *fakeFleet, opts ...func(*fakeGitHub)) *scaleSet {
	gh := &fakeGitHub{}
	for _, opt := range opts {
		opt(gh)
	}

	return newScaleSetOver(f, gh, testScaleSetSpec(f), &recorder{}, discardLogger())
}

// newScaleSetOver returns a scale set of spec over the fleet and GitHub, as the
// Manager makes one, with its GitHub ID set.
func newScaleSetOver(f fleetGroup, gh *fakeGitHub, spec types.ScaleSetSpec, rec metricsRecorder, logger *slog.Logger) *scaleSet {
	s := &scaleSet{
		spec:           spec,
		fleet:          f,
		github:         gh,
		gitHubStatuses: gh,
		installation:   testInstallation,
		interval:       time.Minute,
		metrics:        rec,
		events:         &fakeEvents{},
		logger:         logger,
		listenerLog:    logger,
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

	// sets are GitHub's scale sets by name, and groups its runner groups;
	// nil groups has every group, with ID 1.
	sets   map[string]*ghscaleset.RunnerScaleSet
	groups map[string]int

	// created records what CreateRunnerScaleSet was asked for, and
	// deletedSetIDs what DeleteRunnerScaleSet removed.
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

// GetRunnerByName has every runner, with IDs in the order asked for.
func (g *fakeGitHub) GetRunnerByName(_ context.Context, name string) (*ghscaleset.RunnerReference, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	id := slices.Index(g.runnerIDs, name)
	if id < 0 {
		g.runnerIDs = append(g.runnerIDs, name)
		id = len(g.runnerIDs) - 1
	}

	return &ghscaleset.RunnerReference{ID: id, Name: name}, nil
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

// recorder is a metricsRecorder that keeps what it is told.
type recorder struct {
	mu              sync.Mutex
	paused          []bool
	desired         []int
	created         []string
	removed         []types.RemovalReason
	scaleUpFailures []string
	jobsStarted     int
	jobResults      []string
}

func (r *recorder) SetPaused(_ string, paused bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paused = append(r.paused, paused)
}

func (r *recorder) SetDesiredRunners(_ string, n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.desired = append(r.desired, n)
}

func (r *recorder) CountRunnerCreated(_, provider string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.created = append(r.created, provider)
}

func (r *recorder) CountRunnerRemoved(_, _ string, reason types.RemovalReason) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removed = append(r.removed, reason)
}

func (r *recorder) CountScaleUpFailed(_, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.scaleUpFailures = append(r.scaleUpFailures, reason)
}

func (r *recorder) CountJobStarted(string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobsStarted++
}

func (r *recorder) CountJobCompleted(_, result string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobResults = append(r.jobResults, result)
}

func (r *recorder) SetRunners(string, []types.Runner)     {}
func (r *recorder) SetProviders([]types.ProviderSnapshot) {}

// removals returns the reasons of the removals s counted, in order.
func removals(t *testing.T, s *scaleSet) []types.RemovalReason {
	t.Helper()

	r, ok := s.metrics.(*recorder)
	if !ok {
		t.Fatalf("metrics = %T, want the recorder", s.metrics)
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.removed)
}

// fakeEvents is an events.Recorder that keeps what it is told.
type fakeEvents struct {
	mu     sync.Mutex
	events []types.Event
}

func (e *fakeEvents) Record(event types.Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, event)
}

// recorded returns the events recorded, in order.
func (e *fakeEvents) recorded() []types.Event {
	e.mu.Lock()
	defer e.mu.Unlock()

	return slices.Clone(e.events)
}

// recordedBy returns the events rec, a *fakeEvents, recorded, in order.
func recordedBy(t *testing.T, rec events.Recorder) []types.Event {
	t.Helper()

	e, ok := rec.(*fakeEvents)
	if !ok {
		t.Fatalf("events = %T, want the fake", rec)
	}

	return e.recorded()
}
