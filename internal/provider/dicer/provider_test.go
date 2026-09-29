// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"slices"
	"strings"
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
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

// These tests run the provider with a real Dicer client against in-memory gRPC
// servers, since most of what the provider does is talk to the daemon.

const gib = 1 << 30

// daemon is a Dicer daemon that answers from what it is told to hold.
type daemon struct {
	dicerdv1.UnimplementedDaemonServiceServer

	mu sync.Mutex

	instances []*dicerdv1.Instance

	// delay holds every call up; err fails every call.
	delay time.Duration
	err   error

	// missing makes DeleteInstance report that nothing is there.
	missing bool

	created []*dicerdv1.CreateInstanceRequest
	deleted []*dicerdv1.DeleteInstanceRequest
}

func (d *daemon) delayOrFail(ctx context.Context) error {
	d.mu.Lock()
	delay, err := d.delay, d.err
	d.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return err
}

func (d *daemon) ListInstances(ctx context.Context, _ *dicerdv1.ListInstancesRequest) (*dicerdv1.ListInstancesResponse, error) {
	if err := d.delayOrFail(ctx); err != nil {
		return nil, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	return &dicerdv1.ListInstancesResponse{Instances: d.instances}, nil
}

func (d *daemon) CreateInstance(ctx context.Context, req *dicerdv1.CreateInstanceRequest) (*dicerdv1.Instance, error) {
	if err := d.delayOrFail(ctx); err != nil {
		return nil, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.created = append(d.created, req)

	return &dicerdv1.Instance{Name: req.GetName(), State: dicerdv1.InstanceState_INSTANCE_STATE_RUNNING}, nil
}

func (d *daemon) DeleteInstance(ctx context.Context, req *dicerdv1.DeleteInstanceRequest) (*emptypb.Empty, error) {
	if err := d.delayOrFail(ctx); err != nil {
		return nil, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.missing {
		return nil, status.Errorf(codes.NotFound, "no instance %q", req.GetName())
	}
	d.deleted = append(d.deleted, req)

	return &emptypb.Empty{}, nil
}

func (d *daemon) lastCreated(t *testing.T) *dicerdv1.CreateInstanceRequest {
	t.Helper()

	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.created) != 1 {
		t.Fatalf("created %d instances, want 1", len(d.created))
	}

	return d.created[0]
}

// serve starts d in memory and returns a Dicer client connected to it.
func serve(t *testing.T, d *daemon) client {
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
func newTestProvider(t *testing.T) (*Provider, *daemon) {
	t.Helper()

	d := &daemon{}

	return &Provider{client: serve(t, d), timeout: defaultTimeout, logger: discard()}, d
}

// TestCallsAreBounded checks that a slow daemon is given up on after the
// provider's timeout, rather than holding up a look at the others.
func TestCallsAreBounded(t *testing.T) {
	p, d := newTestProvider(t)
	p.timeout = 50 * time.Millisecond
	d.delay = 5 * time.Second

	start := time.Now()

	if _, err := p.List(context.Background(), nil); err == nil {
		t.Error("List() = nil from a daemon that did not answer in time")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("took %v; the timeout did not bound the calls", elapsed)
	}
}

func TestListFindsOnlyTheSelected(t *testing.T) {
	p, d := newTestProvider(t)

	created := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	d.instances = []*dicerdv1.Instance{
		{Name: "ours-1", Labels: types.RunnerLabels("i", "set", "ours-1", ""),
			State: dicerdv1.InstanceState_INSTANCE_STATE_RUNNING, Vcpus: 2, MemoryBytes: 4 * gib,
			CreateTime: timestamppb.New(created)},
		{Name: "theirs", Labels: map[string]string{"app": "web"}},
		{Name: "other-set", Labels: types.RunnerLabels("i", "other", "other-set", "")},
		{Name: "ours-2", Labels: types.RunnerLabels("i", "set", "ours-2", ""),
			State: dicerdv1.InstanceState_INSTANCE_STATE_PAUSED},
	}

	machines, err := p.List(context.Background(), types.ScaleSetSelector("i", "set"))
	if err != nil {
		t.Fatalf("List() = %v", err)
	}

	slices.SortFunc(machines, func(a, b types.Machine) int { return strings.Compare(a.Name, b.Name) })
	if len(machines) != 2 || machines[0].Name != "ours-1" || machines[1].Name != "ours-2" {
		t.Fatalf("List() = %+v, want ours-1 and ours-2 alone", machines)
	}

	got := machines[0]
	if got.State != types.MachineRunning || !got.CreatedAt.Equal(created) ||
		got.Size != "2 vCPU, 4 GiB" {
		t.Errorf("machine = %+v, want it read from its instance", got)
	}
	if machines[1].State != types.MachineRunning {
		t.Errorf("machine = %+v; a paused runner still holds its runner", machines[1])
	}
	if !machines[1].CreatedAt.IsZero() {
		t.Errorf("CreatedAt = %v; an instance that does not say is not created in 1970", machines[1].CreatedAt)
	}
}

func TestListReportsADaemonThatIsDown(t *testing.T) {
	p, d := newTestProvider(t)
	d.err = status.Error(codes.Unavailable, "down")

	if _, err := p.List(context.Background(), nil); err == nil {
		t.Error("List() = nil from a daemon that is down; what is on it is unknown, not absent")
	}
}

func dicerRunner() RunnerSpec {
	return RunnerSpec{
		ImageRef: "ghcr.io/actions/actions-runner:latest",
		Command:  []string{"/home/runner/run.sh"},
		VCPUs:    4,
		Memory:   8 * gib,
		Disk:     20 * gib,
		Env:      map[string]string{"HTTPS_PROXY": "http://proxy:3128"},
		Mounts:   []Mount{{Type: MountVolume, Source: "cache", Target: "/cache"}},
	}
}

func spec(runner types.RunnerSpec) types.MachineSpec {
	return types.MachineSpec{
		Name:      "set-abcd1234",
		Labels:    types.RunnerLabels("i", "set", "set-abcd1234", ""),
		JITConfig: "encoded-jit-config",
		Runner:    runner,
	}
}

// TestCreateMakesAVMARunner checks the things that make a VM a runner, which
// are Rungar's to decide rather than the configuration's.
func TestCreateMakesAVMARunner(t *testing.T) {
	p, d := newTestProvider(t)

	if err := p.Create(context.Background(), spec(dicerRunner())); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	req := d.lastCreated(t)

	if req.GetName() != "set-abcd1234" || !req.GetStart() {
		t.Errorf("created %q with start=%v, want the runner's name, started", req.GetName(), req.GetStart())
	}
	if req.GetEnv()[jitConfigEnv] != "encoded-jit-config" {
		t.Errorf("the registration is not in %s; a VM without it never becomes a runner", jitConfigEnv)
	}
	if req.GetEnv()["HTTPS_PROXY"] != "http://proxy:3128" {
		t.Error("the configured environment was dropped")
	}
	if req.GetEnv()[allowRootEnv] != "1" {
		t.Error("the runner refuses to start as root unless told it may, and a VM has one user")
	}
	if !req.GetRemoveOnExit() {
		t.Error("a runner that stops while Rungar is not watching would leave its VM behind")
	}
	if mode := req.GetRestartPolicy().GetMode(); mode != dicerdv1.RestartMode_RESTART_MODE_NO {
		t.Errorf("restart policy = %s; a second boot would have an unregistered VM sit there", mode)
	}
	if !types.Matches(req.GetLabels(), types.ScaleSetSelector("i", "set")) {
		t.Error("the instance is not labelled as Rungar's, so a restarted daemon would not find it")
	}
	if req.GetDiskBytes() != 20*gib {
		t.Errorf("disk = %d, want the runner's", req.GetDiskBytes())
	}
	if len(req.GetCmd()) != 1 || req.GetCmd()[0] != "/home/runner/run.sh" {
		t.Errorf("command = %v, want the runner's", req.GetCmd())
	}
	if req.GetVcpus() != 4 || req.GetMemoryBytes() != 8*gib {
		t.Errorf("size = %d vCPU, %d bytes, want the runner's", req.GetVcpus(), req.GetMemoryBytes())
	}
	if len(req.GetMounts()) != 1 || req.GetMounts()[0].GetType() != dicerdv1.MountType_MOUNT_TYPE_VOLUME {
		t.Errorf("mounts = %v, want the configured volume", req.GetMounts())
	}
}

// TestCreateLetsTheConfigurationTurnOffRoot checks that a spec can turn off a
// default Rungar sets for convenience, unlike the registration, which it
// cannot.
func TestCreateLetsTheConfigurationTurnOffRoot(t *testing.T) {
	p, d := newTestProvider(t)

	runner := dicerRunner()
	runner.Env = map[string]string{allowRootEnv: "0", jitConfigEnv: "somebody else's"}

	if err := p.Create(context.Background(), spec(runner)); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	env := d.lastCreated(t).GetEnv()
	if env[allowRootEnv] != "0" {
		t.Errorf("%s = %q, want the configured value", allowRootEnv, env[allowRootEnv])
	}
	if env[jitConfigEnv] != "encoded-jit-config" {
		t.Errorf("%s = %q; a configuration must not break registration", jitConfigEnv, env[jitConfigEnv])
	}
}

type otherRunner struct{}

func (otherRunner) Describe() string { return "n2-standard-4" }

func TestCreateRefusesAnotherProvidersRunner(t *testing.T) {
	p, _ := newTestProvider(t)

	if err := p.Create(context.Background(), spec(otherRunner{})); !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("Create() = %v, want an ErrInvalidArgument", err)
	}
}

// TestCreateSaysWhyItRefused checks that each refusal is put in its class: a
// full daemon is marked so, for the priority hold.
func TestCreateSaysWhyItRefused(t *testing.T) {
	for _, tc := range []struct {
		code  codes.Code
		class error
	}{
		{codes.ResourceExhausted, errdefs.ErrNoCapacity},
		{codes.FailedPrecondition, errdefs.ErrNoCapacity},
		{codes.InvalidArgument, errdefs.ErrInvalidArgument},
		{codes.NotFound, errdefs.ErrInvalidArgument},
	} {
		t.Run(tc.code.String(), func(t *testing.T) {
			p, d := newTestProvider(t)
			d.err = status.Error(tc.code, "the daemon's reason")

			err := p.Create(context.Background(), spec(dicerRunner()))
			if !errors.Is(err, tc.class) {
				t.Errorf("Create() = %v, want it in %v", err, tc.class)
			}
			if err == nil || err.Error() != "the daemon's reason" {
				t.Errorf("Create() = %v, want the daemon's message alone", err)
			}
		})
	}

	for _, code := range []codes.Code{codes.Unavailable, codes.Internal, codes.DeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			p, d := newTestProvider(t)
			d.err = status.Error(code, "down")

			err := p.Create(context.Background(), spec(dicerRunner()))
			if err == nil || errors.Is(err, errdefs.ErrNoCapacity) || errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Fatalf("Create() = %v, want a failure in neither class", err)
			}
			if status.Code(err) != code {
				t.Errorf("Create() = %v, want the daemon's status passed through", err)
			}
		})
	}
}

func TestDeleteForcesTheInstanceOff(t *testing.T) {
	p, d := newTestProvider(t)

	if err := p.Delete(context.Background(), "set-1"); err != nil {
		t.Fatalf("Delete() = %v", err)
	}

	if len(d.deleted) != 1 || d.deleted[0].GetName() != "set-1" {
		t.Fatalf("deleted %v, want set-1", d.deleted)
	}
	if !d.deleted[0].GetForce() {
		t.Error("delete was not forced; a running runner would be refused")
	}
}

// TestDeleteTreatsMissingAsDone checks that an instance that removed itself on
// exit is what was wanted, not an error.
func TestDeleteTreatsMissingAsDone(t *testing.T) {
	p, d := newTestProvider(t)
	d.missing = true

	if err := p.Delete(context.Background(), "set-1"); err != nil {
		t.Errorf("Delete() = %v, want nil for an instance that is already gone", err)
	}
}

func TestDeleteReportsOtherFailures(t *testing.T) {
	p, d := newTestProvider(t)
	d.err = status.Error(codes.Internal, "disk full")

	if err := p.Delete(context.Background(), "set-1"); err == nil {
		t.Error("Delete() = nil, want the daemon's failure")
	}
}

func TestMachineState(t *testing.T) {
	for s, want := range map[dicerdv1.InstanceState]types.MachineState{
		dicerdv1.InstanceState_INSTANCE_STATE_STARTING:   types.MachineStarting,
		dicerdv1.InstanceState_INSTANCE_STATE_RUNNING:    types.MachineRunning,
		dicerdv1.InstanceState_INSTANCE_STATE_PAUSED:     types.MachineRunning,
		dicerdv1.InstanceState_INSTANCE_STATE_STOPPED:    types.MachineStopped,
		dicerdv1.InstanceState_INSTANCE_STATE_STOPPING:   types.MachineStopped,
		dicerdv1.InstanceState_INSTANCE_STATE_RESTARTING: types.MachineStopped,
		dicerdv1.InstanceState_INSTANCE_STATE_FAILED:     types.MachineStopped,
	} {
		if got := machineState(s); got != want {
			t.Errorf("state(%s) = %s, want %s", s, got, want)
		}
	}
}

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }
