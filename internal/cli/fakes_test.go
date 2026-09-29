// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"context"
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
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// fakeDaemon serves the daemon's API from fixed state, so the command line is
// tested end to end over a real socket.
type fakeDaemon struct {
	// The daemon's state, set before a command runs and only read by it.
	status    *rungarv1.DaemonStatus
	scaleSets []*rungarv1.ScaleSet
	runners   []*rungarv1.Runner

	// removeRunnerErr is what RemoveRunner answers for each name, where it is not
	// nil.
	removeRunnerErr map[string]error

	// events are ListEvents' history, and followed what it sends after,
	// to a follower.
	events   []*rungarv1.Event
	followed []*rungarv1.Event

	mu sync.Mutex

	// providerChanges records DisableProvider's and EnableProvider's calls.
	providerChanges []string

	// scaleSetChanges records PauseScaleSet's and ResumeScaleSet's calls.
	scaleSetChanges []string

	// emptyAfter, when not zero, is how many looks at the runners on a
	// provider or of a scale set find them before none are left; looks counts
	// those looks.
	emptyAfter int
	looks      int

	// removeScaleSetResponses are what RemoveScaleSet answers, one call after
	// another.
	removeScaleSetResponses []*rungarv1.RemoveScaleSetResponse
	reconciled              []string

	// eventsAsked is the request ListEvents was last sent.
	eventsAsked *rungarv1.ListEventsRequest
}

var _ rungarv1.RungarServiceServer = (*fakeDaemon)(nil)

// newFakeDaemon returns a daemon running scale set rungar-vm on compute1 and
// compute2, with:
//   - compute1: a limit of 8, a runner of rungar-vm and one of a leftover;
//   - compute2: unreachable;
//   - cloud: held for a scale set of higher priority that found it full;
//   - compute3: failing to create rungar-gpu's runner.
func newFakeDaemon() *fakeDaemon {
	cfg := config.Config{
		GitHub:       config.GitHub{URL: "https://github.com/my-org", TokenPath: "/etc/rungar/token"},
		Installation: "gh-0123456789ab",
	}

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
			State: rungarv1.RunnerState_RUNNER_STATE_BUSY, MachineState: rungarv1.MachineState_MACHINE_STATE_RUNNING,
			GithubStatus: rungarv1.RunnerGitHubStatus_RUNNER_GITHUB_STATUS_BUSY,
			CreateTime:   timestamppb.New(time.Now().Add(-5 * time.Minute)),
			Job:          &rungarv1.RunnerJob{Id: "job-1", Repository: "octo/tests", DisplayName: "build"},
		},
		{
			Name: "gone-vm-1", ScaleSet: "gone-vm", Provider: "compute1", MachineState: rungarv1.MachineState_MACHINE_STATE_RUNNING,
			GithubStatus: rungarv1.RunnerGitHubStatus_RUNNER_GITHUB_STATUS_OFFLINE,
		},
	}

	return &fakeDaemon{
		status: &rungarv1.DaemonStatus{
			Daemon: &rungarv1.Daemon{
				StartTime: started, ConfigFile: "/etc/rungar/config.yaml",
				Installation: cfg.Installation,
				Github: &rungarv1.GitHub{
					Url: cfg.GitHub.URL, Scope: string(cfg.GitHub.Scope()), Credentials: cfg.GitHub.CredentialSummary(),
				},
			},
			ScaleSets: []*rungarv1.ScaleSet{inStatus},
			Providers: []*rungarv1.Provider{
				{
					Name: "compute1", Type: "dicer", Endpoint: "dicer:10.10.0.101:7443", Reachable: true,
					RunnerCount: 2, MaxRunners: 8,
					ScaleSets: []*rungarv1.ProviderScaleSet{{ScaleSet: "rungar-vm"}},
				},
				{
					Name: "compute2", Type: "dicer", Error: "connection refused",
					ScaleSets: []*rungarv1.ProviderScaleSet{{ScaleSet: "rungar-vm"}},
				},
				{
					Name: "cloud", Type: "openstack", Reachable: true, RunnerCount: 3,
					Hold: &rungarv1.Hold{ScaleSet: "rungar-big", Priority: 10, Remaining: durationpb.New(25 * time.Second)},
					ScaleSets: []*rungarv1.ProviderScaleSet{
						{ScaleSet: "rungar-big", BackoffFor: durationpb.New(9 * time.Second), Full: true, Refusals: 1},
					},
				},
				{
					Name: "compute3", Type: "dicer", Reachable: true,
					ScaleSets: []*rungarv1.ProviderScaleSet{
						{
							ScaleSet: "rungar-gpu", BackoffFor: durationpb.New(30 * time.Second), Refusals: 2,
							Refusal: "connection reset",
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

func (f *fakeDaemon) RemoveScaleSet(
	_ context.Context, req *rungarv1.RemoveScaleSetRequest,
) (*rungarv1.RemoveScaleSetResponse, error) {
	if req.GetName() == "rungar-vm" {
		return nil, errdefs.InvalidArgument("scale set %q is configured", req.GetName())
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.removeScaleSetResponses) == 0 {
		return &rungarv1.RemoveScaleSetResponse{}, nil
	}

	resp := f.removeScaleSetResponses[0]
	f.removeScaleSetResponses = f.removeScaleSetResponses[1:]

	return resp, nil
}

func (f *fakeDaemon) Reconcile(_ context.Context, req *rungarv1.ReconcileRequest) (*rungarv1.ReconcileResponse, error) {
	f.mu.Lock()
	f.reconciled = req.GetScaleSets()
	f.mu.Unlock()

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
	for _, pr := range f.status.GetProviders() {
		if pr.GetName() == name {
			return pr, nil
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

	if (req.GetProvider() != "" || req.GetScaleSet() != "") && f.emptyAfter > 0 {
		f.looks++
		if f.looks > f.emptyAfter {
			return &rungarv1.ListRunnersResponse{}, nil
		}
	}

	return &rungarv1.ListRunnersResponse{Runners: f.runners}, nil
}

func (f *fakeDaemon) RemoveRunner(_ context.Context, req *rungarv1.RemoveRunnerRequest) (*rungarv1.Runner, error) {
	if err := f.removeRunnerErr[req.GetName()]; err != nil {
		return nil, err
	}

	// A runner named machineless has no machine, only a registration.
	if req.GetName() == "machineless" {
		return &rungarv1.Runner{Name: req.GetName()}, nil
	}

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

// unversioned is a configuration from before versions were declared, which
// loads, needing no file.
const unversioned = `# Rungar.
github:
  url: https://github.com/my-org
  token: ghp_notarealtoken
providers:
  - name: compute1
    type: dicer
    address: 10.10.0.101:7443
`

// runLocal runs a rungar command that needs no daemon, and returns what it
// printed to stdout and to stderr.
func runLocal(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	var out, errOut bytes.Buffer

	cmd := NewCommand()
	cmd.SetArgs(args)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)

	err = cmd.ExecuteContext(context.Background())

	return out.String(), errOut.String(), err
}

// writeConfigFile writes a configuration file with mode perm and returns its
// path.
func writeConfigFile(t *testing.T, body string, perm os.FileMode) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), perm); err != nil {
		t.Fatal(err)
	}

	return path
}
