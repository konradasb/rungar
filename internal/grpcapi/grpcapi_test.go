// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"
	"net"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/events"
	"github.com/konradasb/rungar/internal/github"
	"github.com/konradasb/rungar/internal/scaleset"
	"github.com/konradasb/rungar/internal/types"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// testInstallation is what the test runners are labelled with.
const testInstallation = "gh-test"

const gib = 1 << 30

// fakeScaleSets is a ScaleSetManager that answers from what it holds, and
// records what it was asked. A name it does not hold is NotFound; err, if
// set, is the answer to every listing, Reconcile and RemoveScaleSet.
type fakeScaleSets struct {
	mu sync.Mutex

	configured []types.ScaleSet
	scaleSets  types.ScaleSetList
	providers  []types.Provider
	runners    types.RunnerList
	removal    types.ScaleSetRemoval
	err        error

	// What the handlers asked for.
	runnerFilter scaleset.RunnerFilter
	runnerGroup  string
	reconciled   []string
}

var _ ScaleSetManager = (*fakeScaleSets)(nil)

func (f *fakeScaleSets) ConfiguredScaleSets() []types.ScaleSet {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.configured)
}

func (f *fakeScaleSets) ScaleSets(context.Context) (types.ScaleSetList, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.scaleSets, f.err
}

func (f *fakeScaleSets) ScaleSet(_ context.Context, name, runnerGroup string) (types.ScaleSet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.runnerGroup = runnerGroup
	for _, set := range f.scaleSets.Items {
		if set.Spec.Name == name {
			return set, nil
		}
	}

	return types.ScaleSet{}, errdefs.NotFound("no scale set %q", name)
}

func (f *fakeScaleSets) Reconcile(_ context.Context, names []string) ([]types.ScaleSet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.reconciled = names
	if f.err != nil {
		return nil, f.err
	}

	return slices.Clone(f.configured), nil
}

func (f *fakeScaleSets) SetPaused(name string, paused bool) (types.ScaleSet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for i := range f.configured {
		if f.configured[i].Spec.Name == name {
			f.configured[i].Status.Paused = paused
			return f.configured[i], nil
		}
	}

	return types.ScaleSet{}, errdefs.NotFound("no scale set %q", name)
}

func (f *fakeScaleSets) RemoveScaleSet(_ context.Context, _, runnerGroup string) (types.ScaleSetRemoval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.runnerGroup = runnerGroup

	return f.removal, f.err
}

func (f *fakeScaleSets) Runners(_ context.Context, filter scaleset.RunnerFilter) (types.RunnerList, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.runnerFilter = filter

	return f.runners, f.err
}

func (f *fakeScaleSets) RemoveRunner(_ context.Context, name string) (types.Runner, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, r := range f.runners.Items {
		if r.Name == name {
			return r, nil
		}
	}

	return types.Runner{}, errdefs.NotFound("no runner %q", name)
}

func (f *fakeScaleSets) Providers(_ context.Context, name string) ([]types.Provider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if name == "" {
		return slices.Clone(f.providers), f.err
	}

	for _, p := range f.providers {
		if p.Snapshot.Name == name {
			return []types.Provider{p}, nil
		}
	}

	return nil, errdefs.NotFound("no provider %q", name)
}

func (f *fakeScaleSets) ProvidersAndRunners(context.Context) ([]types.Provider, types.RunnerList, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.providers), f.runners, f.err
}

func (f *fakeScaleSets) SetProviderDisabled(_ context.Context, name string, disabled bool) (types.Provider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for i := range f.providers {
		if f.providers[i].Snapshot.Name == name {
			f.providers[i].Snapshot.Disabled = disabled
			return f.providers[i], nil
		}
	}

	return types.Provider{}, errdefs.NotFound("no provider %q", name)
}

// setErr sets the answer to every listing, Reconcile and RemoveScaleSet.
func (f *fakeScaleSets) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.err = err
}

// askedRunnerGroup returns the runner group last asked for.
func (f *fakeScaleSets) askedRunnerGroup() string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.runnerGroup
}

// askedRunnerFilter returns the runner filter last asked for.
func (f *fakeScaleSets) askedRunnerFilter() scaleset.RunnerFilter {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.runnerFilter
}

// askedReconciled returns the scale sets last asked to be reconciled.
func (f *fakeScaleSets) askedReconciled() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.reconciled)
}

// runnerSize is a runner spec that is only its size.
type runnerSize types.Resources

func (r runnerSize) Describe() string {
	if r == (runnerSize{}) {
		return ""
	}

	return types.Resources(r).String()
}

// fixture is a Server over fake scale sets and a real, empty event log,
// served over an in-memory connection with the daemon's interceptors.
type fixture struct {
	client    rungarv1.RungarServiceClient
	server    *grpc.Server
	scaleSets *fakeScaleSets
	events    *events.Log
	info      DaemonInfo
}

// newFixture makes a fixture whose scale sets hold nothing.
func newFixture(t *testing.T) *fixture {
	t.Helper()

	f := &fixture{
		scaleSets: &fakeScaleSets{},
		events:    newEventLog(t),
		info: DaemonInfo{
			StartedAt:         time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
			ConfigFile:        "/etc/rungar/config.yaml",
			Installation:      testInstallation,
			GitHubURL:         "https://github.com/my-org",
			GitHubScope:       github.ScopeOrganisation,
			GitHubCredentials: "GitHub personal access token (given inline)",
		},
	}
	f.client, f.server = serve(t, Config{ScaleSets: f.scaleSets, Events: f.events, Info: f.info})

	return f
}

// newEventLog opens an empty event log, closed when the test ends.
func newEventLog(t *testing.T) *events.Log {
	t.Helper()

	eventLog, err := events.Open(events.Config{File: filepath.Join(t.TempDir(), "events.jsonl")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eventLog.Close() })

	return eventLog
}

// serve serves a Server over cfg on an in-memory connection, with the
// daemon's interceptors, and returns a client of it and the gRPC server.
func serve(t *testing.T, cfg Config) (rungarv1.RungarServiceClient, *grpc.Server) {
	t.Helper()

	listener := bufconn.Listen(1 << 20)

	server := grpc.NewServer(
		grpc.ChainUnaryInterceptor(UnaryErrorCodeInterceptor),
		grpc.ChainStreamInterceptor(StreamErrorCodeInterceptor),
	)
	NewServer(cfg).Register(server)

	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return rungarv1.NewRungarServiceClient(conn), server
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
