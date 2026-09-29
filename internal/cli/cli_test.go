// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/konradasb/rungar/internal/config"
	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/grpcapi"
	"github.com/konradasb/rungar/internal/types"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// fakeDaemon serves the daemon's API from fixed state, so the command line is
// tested end to end over a real socket.
type fakeDaemon struct {
	mu sync.Mutex

	status    *rungarv1.DaemonStatus
	scaleSets []*rungarv1.ScaleSet
	runners   []*rungarv1.Runner

	// deleteRunnerErr is what DeleteRunner answers for each name, where it is not
	// nil.
	deleteRunnerErr map[string]error
	deletedRunners  []string

	// providerChanges records DisableProvider's and EnableProvider's calls. When
	// draining, the runners on a provider are gone after drainAfter looks at
	// them.
	providerChanges []string
	draining        bool
	drainAfter      int
	looks           int

	// scaleSetChanges records PauseScaleSet's and ResumeScaleSet's calls.
	// Draining applies to a scale set's runners too.
	scaleSetChanges []string

	// deleteScaleSetResponses are what DeleteScaleSet answers, one call after another.
	deleteScaleSetResponses []*rungarv1.DeleteScaleSetResponse
	reconciled              []string

	// events are ListEvents' history, and followed what it sends after,
	// to a follower. eventsAsked is the request it was last sent.
	events      []*rungarv1.Event
	followed    []*rungarv1.Event
	eventsAsked *rungarv1.ListEventsRequest
}

var _ rungarv1.RungarServiceServer = (*fakeDaemon)(nil)

// newFakeDaemon returns a daemon running scale set rungar-vm on compute1 and
// compute2, with:
//   - compute1: a limit of 8, a runner of rungar-vm and one of a leftover;
//   - compute2: unreachable;
//   - cloud: held for a scale set of higher priority that found it full, and
//     reporting nothing spare;
//   - compute3: failing to make rungar-gpu's runner.
func newFakeDaemon() *fakeDaemon {
	cfg := config.Default()
	cfg.GitHub = config.GitHubConfig{URL: "https://github.com/my-org", TokenPath: "/etc/rungar/token"}

	started := timestamppb.New(time.Now().Add(-3 * time.Hour))
	const runnerSize = "2 vCPU, 4 GiB"

	rungarVM := func() *rungarv1.ScaleSet {
		return &rungarv1.ScaleSet{
			Name: "rungar-vm", Configured: true, Phase: rungarv1.ScaleSetPhase_SCALE_SET_PHASE_LISTENING,
			Labels: []string{"rungar-vm", "linux"}, Providers: []string{"compute1", "compute2"},
			MaxRunners: 4, RunsOn: "runs-on: rungar-vm",
			RunnerSizes: []*rungarv1.RunnerSize{
				{Provider: "compute1", Description: runnerSize},
				{Provider: "compute2", Description: runnerSize},
			},
		}
	}

	inStatus := rungarVM()
	inStatus.DesiredRunners = 2
	inStatus.RunnerCounts = &rungarv1.RunnerCounts{Busy: 1}
	inStatus.HoldingBackReason = `scale set "rungar-big" is waiting for room and takes priority`

	inListing := rungarVM()
	inListing.FleetRunners = 1
	inListing.Github = &rungarv1.GitHubScaleSet{
		Found: true, Id: 7, RunnerGroup: "Default", Labels: []string{"rungar-vm", "linux"},
		Statistics: &rungarv1.ScaleSetStatistics{AssignedJobs: 3, RunningJobs: 2},
	}

	runners := []*rungarv1.Runner{
		{
			Name: "rungar-vm-1", ScaleSet: "rungar-vm", Provider: "compute1",
			State: rungarv1.RunnerState_RUNNER_STATE_BUSY, JobId: "job-1", MachineState: string(types.MachineRunning),
			GithubStatus: rungarv1.RunnerGitHubStatus_RUNNER_GIT_HUB_STATUS_BUSY,
			CreateTime:   timestamppb.New(time.Now().Add(-5 * time.Minute)),
		},
		{
			Name: "gone-vm-1", ScaleSet: "gone-vm", Provider: "compute1", MachineState: string(types.MachineRunning),
			GithubStatus: rungarv1.RunnerGitHubStatus_RUNNER_GIT_HUB_STATUS_OFFLINE,
		},
	}

	return &fakeDaemon{
		status: &rungarv1.DaemonStatus{
			Daemon: &rungarv1.Daemon{
				StartTime: started, ConfigFile: "/etc/rungar/config.yaml",
				Installation: cfg.Installation,
				Github: &rungarv1.GitHub{
					Url: cfg.GitHub.URL, Scope: cfg.GitHub.Scope(), Credentials: cfg.GitHub.Summary(),
				},
			},
			ScaleSets: []*rungarv1.ScaleSet{inStatus},
			Providers: []*rungarv1.Provider{
				{
					Name: "compute1", Type: "dicer", Endpoint: "10.10.0.101:7443", Reachable: true,
					RunnerCount: 2, MaxRunners: 8, ScaleSets: []string{"rungar-vm"},
					Placements: []*rungarv1.ProviderScaleSet{{ScaleSet: "rungar-vm"}},
				},
				{
					Name: "compute2", Type: "dicer", Error: "connection refused", ScaleSets: []string{"rungar-vm"},
					Placements: []*rungarv1.ProviderScaleSet{{ScaleSet: "rungar-vm"}},
				},
				{
					Name: "cloud", Type: "openstack", Reachable: true, RunnerCount: 3,
					Hold:      &rungarv1.Hold{ScaleSet: "rungar-big", Priority: 10, Remaining: durationpb.New(25 * time.Second)},
					ScaleSets: []string{"rungar-big"},
					Placements: []*rungarv1.ProviderScaleSet{
						{ScaleSet: "rungar-big", BackoffFor: durationpb.New(9 * time.Second), Full: true, Failures: 1},
					},
				},
				{
					Name: "compute3", Type: "dicer", Reachable: true,
					ScaleSets: []string{"rungar-gpu"},
					Placements: []*rungarv1.ProviderScaleSet{
						{
							ScaleSet: "rungar-gpu", BackoffFor: durationpb.New(30 * time.Second), Failures: 2,
							Failure: "connection reset",
						},
					},
				},
			},
			Runners: runners,
		},
		scaleSets: []*rungarv1.ScaleSet{
			inListing,
			{Name: "gone-vm", FleetRunners: 1, Github: &rungarv1.GitHubScaleSet{}},
		},
		runners: runners,
	}
}

func (f *fakeDaemon) GetStatus(context.Context, *rungarv1.GetStatusRequest) (*rungarv1.DaemonStatus, error) {
	return f.status, nil
}

func (f *fakeDaemon) ListScaleSets(
	context.Context, *rungarv1.ListScaleSetsRequest,
) (*rungarv1.ListScaleSetsResponse, error) {
	return &rungarv1.ListScaleSetsResponse{
		ScaleSets:            f.scaleSets,
		UnreachableProviders: map[string]string{"compute2": "connection refused"},
	}, nil
}

func (f *fakeDaemon) GetScaleSet(_ context.Context, req *rungarv1.GetScaleSetRequest) (*rungarv1.ScaleSet, error) {
	for _, set := range f.scaleSets {
		if set.GetName() == req.GetName() {
			return set, nil
		}
	}

	return nil, errdefs.NotFound("no scale set %q", req.GetName())
}

func (f *fakeDaemon) DeleteScaleSet(
	_ context.Context, req *rungarv1.DeleteScaleSetRequest,
) (*rungarv1.DeleteScaleSetResponse, error) {
	if req.GetName() == "rungar-vm" {
		return nil, errdefs.InvalidArgument("scale set %q is configured", req.GetName())
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.deleteScaleSetResponses) == 0 {
		return &rungarv1.DeleteScaleSetResponse{}, nil
	}

	resp := f.deleteScaleSetResponses[0]
	f.deleteScaleSetResponses = f.deleteScaleSetResponses[1:]

	return resp, nil
}

func (f *fakeDaemon) Reconcile(_ context.Context, req *rungarv1.ReconcileRequest) (*rungarv1.ReconcileResponse, error) {
	f.reconciled = req.GetScaleSets()
	return &rungarv1.ReconcileResponse{ScaleSets: f.status.GetScaleSets()}, nil
}

func (f *fakeDaemon) ListProviders(
	context.Context, *rungarv1.ListProvidersRequest,
) (*rungarv1.ListProvidersResponse, error) {
	return &rungarv1.ListProvidersResponse{Providers: f.status.GetProviders()}, nil
}

func (f *fakeDaemon) GetProvider(_ context.Context, req *rungarv1.GetProviderRequest) (*rungarv1.Provider, error) {
	return f.provider(req.GetName())
}

func (f *fakeDaemon) provider(name string) (*rungarv1.Provider, error) {
	for _, p := range f.status.GetProviders() {
		if p.GetName() == name {
			return p, nil
		}
	}

	return nil, errdefs.NotFound("no provider %q", name)
}

func (f *fakeDaemon) DisableProvider(
	_ context.Context, req *rungarv1.DisableProviderRequest,
) (*rungarv1.Provider, error) {
	f.mu.Lock()
	f.providerChanges = append(f.providerChanges, "disable "+req.GetName())
	f.mu.Unlock()

	return f.provider(req.GetName())
}

func (f *fakeDaemon) EnableProvider(
	_ context.Context, req *rungarv1.EnableProviderRequest,
) (*rungarv1.Provider, error) {
	f.mu.Lock()
	f.providerChanges = append(f.providerChanges, "enable "+req.GetName())
	f.mu.Unlock()

	return f.provider(req.GetName())
}

func (f *fakeDaemon) PauseScaleSet(
	_ context.Context, req *rungarv1.PauseScaleSetRequest,
) (*rungarv1.ScaleSet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scaleSetChanges = append(f.scaleSetChanges, "pause "+req.GetName())

	return &rungarv1.ScaleSet{Name: req.GetName(), Configured: true, Paused: true}, nil
}

func (f *fakeDaemon) ResumeScaleSet(
	_ context.Context, req *rungarv1.ResumeScaleSetRequest,
) (*rungarv1.ScaleSet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scaleSetChanges = append(f.scaleSetChanges, "resume "+req.GetName())

	return &rungarv1.ScaleSet{Name: req.GetName(), Configured: true}, nil
}

func (f *fakeDaemon) ListRunners(
	_ context.Context, req *rungarv1.ListRunnersRequest,
) (*rungarv1.ListRunnersResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if (req.GetProvider() != "" || req.GetScaleSet() != "") && f.draining {
		f.looks++
		if f.looks > f.drainAfter {
			return &rungarv1.ListRunnersResponse{}, nil
		}
	}

	return &rungarv1.ListRunnersResponse{Runners: f.runners}, nil
}

func (f *fakeDaemon) DeleteRunner(_ context.Context, req *rungarv1.DeleteRunnerRequest) (*rungarv1.Runner, error) {
	if err := f.deleteRunnerErr[req.GetName()]; err != nil {
		return nil, err
	}

	f.deletedRunners = append(f.deletedRunners, req.GetName())

	return &rungarv1.Runner{Name: req.GetName(), Provider: "compute1"}, nil
}

func (f *fakeDaemon) ListEvents(
	req *rungarv1.ListEventsRequest, stream grpc.ServerStreamingServer[rungarv1.ListEventsResponse],
) error {
	f.mu.Lock()
	f.eventsAsked = req
	f.mu.Unlock()

	if err := stream.Send(&rungarv1.ListEventsResponse{Events: f.events, CaughtUp: true}); err != nil {
		return err
	}
	if !req.GetFollow() {
		return nil
	}

	for _, e := range f.followed {
		if err := stream.Send(&rungarv1.ListEventsResponse{Events: []*rungarv1.Event{e}}); err != nil {
			return err
		}
	}

	return nil
}

// harness is the command line connected to a fake daemon on a socket.
type harness struct {
	daemon *fakeDaemon
	socket string
	out    *bytes.Buffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	// Short, since a socket's path is limited to about a hundred bytes and
	// a test's temporary directory can be longer.
	dir, err := os.MkdirTemp("", "rungar")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "rungar.sock")

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}

	d := newFakeDaemon()
	server := grpc.NewServer(
		grpc.ChainUnaryInterceptor(grpcapi.UnaryErrorCodeInterceptor),
		grpc.ChainStreamInterceptor(grpcapi.StreamErrorCodeInterceptor),
	)
	rungarv1.RegisterRungarServiceServer(server, d)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	saved := pollInterval
	pollInterval = time.Millisecond
	t.Cleanup(func() { pollInterval = saved })

	return &harness{daemon: d, socket: socket, out: &bytes.Buffer{}}
}

// run runs a rungar command against the fake daemon, as a person would.
func (h *harness) run(args ...string) error {
	cmd := NewCommand()
	cmd.SetArgs(append(args, "--socket", h.socket))
	cmd.SetOut(h.out)
	cmd.SetErr(h.out)

	return cmd.ExecuteContext(context.Background())
}

// says checks the output mentions every fragment.
func (h *harness) says(t *testing.T, fragments ...string) {
	t.Helper()

	for _, want := range fragments {
		if !strings.Contains(h.out.String(), want) {
			t.Errorf("the output does not say %q:\n%s", want, h.out)
		}
	}
}

// TestErrorsAreThePlainMessage checks an error the daemon sends reads as its
// message alone.
func TestErrorsAreThePlainMessage(t *testing.T) {
	h := newHarness(t)

	err := h.run("scale-sets", "inspect", "nowhere")
	if got := errorMessage(err); got != `no scale set "nowhere"` {
		t.Errorf("errorMessage() = %q, want the daemon's words alone", got)
	}
}

// TestNoSocketSaysRungarIsNotRunning checks a command with no daemon to ask
// says so, and how to point it at another socket.
func TestNoSocketSaysRungarIsNotRunning(t *testing.T) {
	cmd := NewCommand()
	cmd.SetArgs([]string{"status", "--socket", filepath.Join(t.TempDir(), "rungar.sock")})
	cmd.SetOut(io.Discard)

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "is rungar running?") || !strings.Contains(err.Error(), socketEnv) {
		t.Errorf("rungar status = %v, want it to say rungar is not running, and how to say where it is", err)
	}
}

func TestSocketPath(t *testing.T) {
	cmd := NewCommand()

	t.Setenv(socketEnv, "")
	if got := socketPath(cmd); got != config.DefaultSocket {
		t.Errorf("socketPath() = %q, want the default", got)
	}

	t.Setenv(socketEnv, "/tmp/from-env.sock")
	if got := socketPath(cmd); got != "/tmp/from-env.sock" {
		t.Errorf("socketPath() = %q, want $%s", got, socketEnv)
	}

	if err := cmd.ParseFlags([]string{"--socket", "/tmp/from-flag.sock"}); err != nil {
		t.Fatal(err)
	}
	if got := socketPath(cmd); got != "/tmp/from-flag.sock" {
		t.Errorf("socketPath() = %q, want --socket over the environment", got)
	}
}
