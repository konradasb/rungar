// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	ghscaleset "github.com/actions/scaleset"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/konradasb/rungar/internal/events"
	"github.com/konradasb/rungar/internal/fleet"
	"github.com/konradasb/rungar/internal/github"
	"github.com/konradasb/rungar/internal/scaleset"
	"github.com/konradasb/rungar/internal/types"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// testInstallation is what the fixture's runners are labelled with.
const testInstallation = "gh-test"

const gib = int64(1) << 30

// fakeProvider is an in-memory provider.
type fakeProvider struct {
	mu sync.Mutex

	machines []types.Machine

	// listErr is what List answers with, if anything.
	listErr error
}

func (b *fakeProvider) List(_ context.Context, selector map[string]string) ([]types.Machine, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.listErr != nil {
		return nil, b.listErr
	}

	var out []types.Machine
	for _, m := range b.machines {
		if types.Matches(m.Labels, selector) {
			out = append(out, m)
		}
	}

	return out, nil
}

func (b *fakeProvider) Create(context.Context, types.MachineSpec) error { return nil }
func (b *fakeProvider) Close() error                                    { return nil }

func (b *fakeProvider) Delete(_ context.Context, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.machines = slices.DeleteFunc(b.machines, func(m types.Machine) bool { return m.Name == name })

	return nil
}

// fakeGitHub is an in-memory GitHub, serving both the scale set API and the
// REST API.
type fakeGitHub struct {
	mu sync.Mutex

	sets    map[string]*ghscaleset.RunnerScaleSet
	runners []github.Runner

	// listErr is what listing the runners answers with, if anything.
	listErr error

	// deleted are the IDs of the scale sets removed.
	deleted []int
}

func (g *fakeGitHub) GetRunnerGroupByName(_ context.Context, name string) (*ghscaleset.RunnerGroup, error) {
	return &ghscaleset.RunnerGroup{ID: 1, Name: name}, nil
}

func (g *fakeGitHub) GetRunnerScaleSet(_ context.Context, _ int, name string) (*ghscaleset.RunnerScaleSet, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.sets[name], nil
}

func (g *fakeGitHub) CreateRunnerScaleSet(context.Context, *ghscaleset.RunnerScaleSet) (*ghscaleset.RunnerScaleSet,
	error,
) {
	return nil, errors.New("not in these tests")
}

func (g *fakeGitHub) DeleteRunnerScaleSet(_ context.Context, id int) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.deleted = append(g.deleted, id)
	for name, set := range g.sets {
		if set.ID == id {
			delete(g.sets, name)
		}
	}

	return nil
}

func (g *fakeGitHub) GenerateJitRunnerConfig(context.Context, *ghscaleset.RunnerScaleSetJitRunnerSetting, int,
) (*ghscaleset.RunnerScaleSetJitRunnerConfig, error) {
	return nil, errors.New("not in these tests")
}

func (g *fakeGitHub) GetRunnerByName(_ context.Context, name string) (*ghscaleset.RunnerReference, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	for _, r := range g.runners {
		if r.Name == name {
			return &ghscaleset.RunnerReference{ID: int(r.ID), Name: r.Name}, nil
		}
	}

	return nil, nil //nolint:nilnil // as the scale set client answers for a runner GitHub does not have
}

// RemoveRunner refuses a runner that is running a job, as GitHub does.
func (g *fakeGitHub) RemoveRunner(_ context.Context, id int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	i := slices.IndexFunc(g.runners, func(r github.Runner) bool { return r.ID == id })
	switch {
	case i < 0:
		return nil
	case g.runners[i].Busy:
		return ghscaleset.JobStillRunningError
	}

	g.runners = slices.Delete(g.runners, i, i+1)

	return nil
}

func (g *fakeGitHub) ListRunners(context.Context) ([]github.Runner, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.listErr != nil {
		return nil, g.listErr
	}

	return slices.Clone(g.runners), nil
}

// setBusy has GitHub say a runner is running a job.
func (g *fakeGitHub) setBusy(name string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	for i := range g.runners {
		if g.runners[i].Name == name {
			g.runners[i].Busy = true
		}
	}
}

// runnerSize is a runner spec that is only its size.
type runnerSize types.Resources

func (r runnerSize) Describe() string {
	if r == (runnerSize{}) {
		return ""
	}

	return types.Resources(r).String()
}

// fixture is a Server over scale sets that have not been run, served over an
// in-memory connection with the daemon's interceptors: scale set rungar-vm on
// compute1, which holds the runners given, and GitHub holding rungar-vm and
// gone-vm, a scale set no longer configured.
type fixture struct {
	client  rungarv1.RungarServiceClient
	server  *grpc.Server
	compute *fakeProvider
	github  *fakeGitHub
	events  *events.Log
	info    types.DaemonInfo
}

// newFixture makes a fixture over runners written scaleSet/name, each online
// and idle on GitHub.
func newFixture(t *testing.T, runners ...string) *fixture {
	t.Helper()

	f := &fixture{
		compute: &fakeProvider{},
		github: &fakeGitHub{sets: map[string]*ghscaleset.RunnerScaleSet{
			"rungar-vm": {ID: 7, Name: "rungar-vm", RunnerGroupName: types.DefaultRunnerGroup,
				Labels:     []ghscaleset.Label{{Name: "rungar-vm"}, {Name: "linux"}},
				Statistics: &ghscaleset.RunnerScaleSetStatistic{TotalAssignedJobs: 3, TotalRunningJobs: 2}},
			"gone-vm": {ID: 9, Name: "gone-vm", RunnerGroupName: types.DefaultRunnerGroup},
		}},
		info: types.DaemonInfo{
			StartTime:         time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
			ConfigFile:        "/etc/rungar/config.yaml",
			Installation:      testInstallation,
			GitHubURL:         "https://github.com/my-org",
			GitHubScope:       github.ScopeOrganisation,
			GitHubCredentials: "GitHub PAT (given inline)",
		},
	}

	for i, r := range runners {
		scaleSet, name, _ := strings.Cut(r, "/")
		f.compute.machines = append(f.compute.machines, types.Machine{
			Name:      name,
			Labels:    types.RunnerLabels(testInstallation, scaleSet, name, ""),
			State:     types.MachineRunning,
			Size:      "2 vCPU, 4 GiB",
			CreatedAt: time.Now().Add(-time.Minute),
		})
		f.github.runners = append(f.github.runners, github.Runner{ID: int64(i + 1), Name: name, Status: "online"})
	}

	var err error
	f.events, err = events.Open(events.Config{Path: filepath.Join(t.TempDir(), "events.jsonl")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.events.Close() })

	fl := fleet.New([]fleet.Provider{{
		Name: "compute1", Type: "dicer", Weight: 1, Endpoint: "10.0.0.1:7443", Backend: f.compute,
	}}, fleet.Options{Events: f.events})

	spec := types.ScaleSetSpec{
		Name:         "rungar-vm",
		Labels:       []string{"linux"},
		RunnerGroup:  types.DefaultRunnerGroup,
		MinRunners:   1,
		MaxRunners:   4,
		Priority:     2,
		Providers:    []types.ProviderRef{{Name: "compute1"}},
		Placement:    types.PlacementSpread,
		StartTimeout: time.Minute,
		RunnerSpecs:  map[string]types.RunnerSpec{"compute1": runnerSize{VCPUs: 2, MemoryBytes: 4 * gib}},
	}

	sets, err := scaleset.New(scaleset.Config{
		ScaleSets:         []types.ScaleSetSpec{spec},
		Installation:      testInstallation,
		ReconcileInterval: time.Minute,
		Fleet:             fl,
		GitHub:            f.github,
		NewScaleSetClient: func() (*ghscaleset.Client, error) {
			return ghscaleset.NewClientWithPersonalAccessToken(ghscaleset.NewClientWithPersonalAccessTokenConfig{
				GitHubConfigURL: "https://github.com/my-org", PersonalAccessToken: "test",
			})
		},
		GitHubRunners: f.github,
		Events:        f.events,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}

	listener := bufconn.Listen(1 << 20)

	f.server = grpc.NewServer(
		grpc.ChainUnaryInterceptor(UnaryErrorCodeInterceptor),
		grpc.ChainStreamInterceptor(StreamErrorCodeInterceptor),
	)
	NewServer(Config{ScaleSets: sets, Events: f.events, Info: f.info}).Register(f.server)

	go func() { _ = f.server.Serve(listener) }()
	t.Cleanup(f.server.Stop)

	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	f.client = rungarv1.NewRungarServiceClient(conn)

	return f
}

// wantCode fails the test unless err is a status of the code wanted.
func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()

	if got := status.Code(err); got != want {
		t.Fatalf("error = %v (%v), want %v", err, got, want)
	}
}

// TestRegisterServesEveryMethod checks that Register puts the whole
// RungarService on the server, so that no call the command line makes is
// answered Unimplemented.
func TestRegisterServesEveryMethod(t *testing.T) {
	f := newFixture(t)

	info, ok := f.server.GetServiceInfo()[rungarv1.RungarService_ServiceDesc.ServiceName]
	if !ok {
		t.Fatal("RungarService is not registered")
	}

	desc := rungarv1.RungarService_ServiceDesc
	if got, want := len(info.Methods), len(desc.Methods)+len(desc.Streams); got != want {
		t.Errorf("registered %d methods, want %d", got, want)
	}
}

// TestHandlerErrorsArriveAsTheirClass checks that a handler's error reaches the
// client as a status of its class through the interceptors, with its message.
func TestHandlerErrorsArriveAsTheirClass(t *testing.T) {
	f := newFixture(t)

	_, err := f.client.GetProvider(context.Background(), &rungarv1.GetProviderRequest{Name: "nowhere"})
	wantCode(t, err, codes.NotFound)

	if msg := status.Convert(err).Message(); !strings.Contains(msg, `no provider "nowhere"`) {
		t.Errorf("message = %q, want it to name the provider", msg)
	}
}
