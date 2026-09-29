// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"context"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	dicerclient "github.com/konradasb/dicer"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/konradasb/rungar/internal/types"
)

// These tests run the provider with a real Dicer client against in-memory gRPC
// servers, since most of what the provider does is talk to the daemon.

const gib = 1 << 30

// fakeDicer is a Dicer daemon that answers from what it is told to hold.
type fakeDicer struct {
	dicerdv1.UnimplementedDaemonServiceServer

	mu sync.Mutex

	instances []*dicerdv1.Instance

	// delay holds every call up; err fails every call.
	delay time.Duration
	err   error

	// missing makes DeleteInstance report that nothing is there.
	missing bool

	// held are the images the host has, by reference. A pull adds to them.
	held map[string]bool

	// pullDelay holds up a pull, and pullErr fails it; createDelay holds up
	// CreateInstance, and createErr fails it.
	pullDelay   time.Duration
	pullErr     error
	createDelay time.Duration
	createErr   error

	// pulls, creates and deletes are the requests served, in order; tests
	// read them through pullRequests, lastCreate, createCount and
	// deleteRequests.
	pulls   []string
	creates []*dicerdv1.CreateInstanceRequest
	deletes []*dicerdv1.DeleteInstanceRequest
}

// delayOrFail holds a call up for d.delay, then fails it with d.err.
func (d *fakeDicer) delayOrFail(ctx context.Context) error {
	d.mu.Lock()
	delay, failure := d.delay, d.err
	d.mu.Unlock()

	if err := wait(ctx, delay); err != nil {
		return err
	}

	return failure
}

func (d *fakeDicer) ListInstances(
	ctx context.Context, _ *dicerdv1.ListInstancesRequest,
) (*dicerdv1.ListInstancesResponse, error) {
	if err := d.delayOrFail(ctx); err != nil {
		return nil, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	return &dicerdv1.ListInstancesResponse{Instances: d.instances}, nil
}

func (d *fakeDicer) CreateInstance(
	ctx context.Context, req *dicerdv1.CreateInstanceRequest,
) (*dicerdv1.Instance, error) {
	if err := d.delayOrFail(ctx); err != nil {
		return nil, err
	}

	d.mu.Lock()
	delay := d.createDelay
	d.mu.Unlock()

	if err := wait(ctx, delay); err != nil {
		return nil, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.createErr != nil {
		return nil, d.createErr
	}
	// As the daemon does: told not to pull, it refuses an image it lacks.
	if req.GetPullPolicy() == dicerdv1.PullPolicy_PULL_POLICY_NEVER && !d.held[req.GetImageRef()] {
		return nil, status.Errorf(codes.NotFound, "image %q is not on this host", req.GetImageRef())
	}
	d.creates = append(d.creates, req)

	return &dicerdv1.Instance{Name: req.GetName(), State: dicerdv1.InstanceState_INSTANCE_STATE_RUNNING}, nil
}

func (d *fakeDicer) DeleteInstance(
	ctx context.Context, req *dicerdv1.DeleteInstanceRequest,
) (*emptypb.Empty, error) {
	if err := d.delayOrFail(ctx); err != nil {
		return nil, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.missing {
		return nil, status.Errorf(codes.NotFound, "no instance %q", req.GetName())
	}
	d.deletes = append(d.deletes, req)

	return &emptypb.Empty{}, nil
}

func (d *fakeDicer) GetImage(ctx context.Context, req *dicerdv1.GetImageRequest) (*dicerdv1.Image, error) {
	if err := d.delayOrFail(ctx); err != nil {
		return nil, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.held[req.GetRef()] {
		return nil, status.Errorf(codes.NotFound, "no image %q", req.GetRef())
	}

	return &dicerdv1.Image{Name: req.GetRef()}, nil
}

func (d *fakeDicer) PullImage(
	req *dicerdv1.PullImageRequest, stream grpc.ServerStreamingServer[dicerdv1.PullImageProgress],
) error {
	d.mu.Lock()
	delay, failure := d.pullDelay, d.pullErr
	d.pulls = append(d.pulls, req.GetRef())
	d.mu.Unlock()

	if err := stream.Send(&dicerdv1.PullImageProgress{Stage: dicerdv1.PullStage_PULL_STAGE_DOWNLOADING}); err != nil {
		return err
	}
	if err := wait(stream.Context(), delay); err != nil {
		return err
	}
	if failure != nil {
		return failure
	}

	d.mu.Lock()
	if d.held == nil {
		d.held = map[string]bool{}
	}
	d.held[req.GetRef()] = true
	d.mu.Unlock()

	return stream.Send(&dicerdv1.PullImageProgress{Image: &dicerdv1.Image{Name: req.GetRef()}})
}

// wait waits for delay, or until ctx is done.
func wait(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}

	select {
	case <-time.After(delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// lastCreate returns the one CreateInstance request served, failing the test
// if there is not exactly one.
func (d *fakeDicer) lastCreate(t *testing.T) *dicerdv1.CreateInstanceRequest {
	t.Helper()

	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.creates) != 1 {
		t.Fatalf("created %d instances, want 1", len(d.creates))
	}

	return d.creates[0]
}

// pullRequests returns the image of each pull served, in order.
func (d *fakeDicer) pullRequests() []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	return slices.Clone(d.pulls)
}

// createCount returns how many instances were created.
func (d *fakeDicer) createCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	return len(d.creates)
}

// deleteRequests returns the DeleteInstance requests served, in order.
func (d *fakeDicer) deleteRequests() []*dicerdv1.DeleteInstanceRequest {
	d.mu.Lock()
	defer d.mu.Unlock()

	return slices.Clone(d.deletes)
}

// serve starts d in memory and returns a Dicer client connected to it.
func serve(t *testing.T, d *fakeDicer) dicerAPI {
	t.Helper()

	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	dicerdv1.RegisterDaemonServiceServer(server, d)

	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	c, err := dicerclient.NewClient(
		dicerclient.WithAddress("passthrough:///bufnet"),
		dicerclient.WithDialOptions(grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		})))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	return c
}

// newTestProvider is a provider of one daemon, served in memory.
func newTestProvider(t *testing.T) (*Provider, *fakeDicer) {
	t.Helper()

	d := &fakeDicer{}

	config := &Config{Timeout: defaultTimeout}

	return config.open(nil, serve(t, d)), d
}

// testRunner returns a runner block of every kind of setting.
func testRunner() RunnerSpec {
	return RunnerSpec{
		Image:   "ghcr.io/actions/actions-runner:latest",
		Command: []string{"/home/runner/run.sh"},
		VCPUs:   4,
		Memory:  8 * gib,
		Disk:    20 * gib,
		Env:     map[string]string{"HTTPS_PROXY": "http://proxy:3128"},
		Mounts:  []Mount{{Type: MountVolume, Source: "cache", Target: "/cache"}},
	}
}

// testMachine returns the machine spec of a runner of scale set, as Rungar
// creates it.
func testMachine(name, scaleSet string) types.MachineSpec {
	return types.MachineSpec{
		Name:      name,
		Labels:    types.RunnerLabels("i", scaleSet, name, ""),
		JITConfig: "encoded-jit-config",
		Runner:    testRunner(),
	}
}

// otherRunner is a runner block of another provider type.
type otherRunner struct{}

func (otherRunner) Describe() string { return "other" }
