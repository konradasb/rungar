// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"slices"
	"strings"
	"testing"
	"time"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/konradasb/rungar/internal/types"
)

// TestCallsAreBounded checks that a slow daemon is given up on after the
// provider's timeout, rather than holding up a look at the others.
func TestCallsAreBounded(t *testing.T) {
	p, d := newTestProvider(t)
	p.config.Timeout = 50 * time.Millisecond
	d.delay = 5 * time.Second

	start := time.Now()

	if _, err := p.List(t.Context(), nil); err == nil {
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

	machines, err := p.List(t.Context(), types.ScaleSetSelector("i", "set"))
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
		t.Errorf("machine = %+v, want running: a paused instance still holds its runner", machines[1])
	}
	if !machines[1].CreatedAt.IsZero() {
		t.Errorf("CreatedAt = %v; an instance that does not say is not created in 1970", machines[1].CreatedAt)
	}
}

func TestListReportsADaemonThatIsDown(t *testing.T) {
	p, d := newTestProvider(t)
	d.err = status.Error(codes.Unavailable, "down")

	if _, err := p.List(t.Context(), nil); status.Code(err) != codes.Unavailable {
		t.Errorf("List() = %v from a daemon that is down, want its error: what is on it is unknown, "+
			"not absent", err)
	}
}

func TestDeleteForcesTheInstanceOff(t *testing.T) {
	p, d := newTestProvider(t)

	if err := p.Delete(t.Context(), "set-1"); err != nil {
		t.Fatalf("Delete() = %v", err)
	}

	deleted := d.deleteRequests()
	if len(deleted) != 1 || deleted[0].GetName() != "set-1" {
		t.Fatalf("deleted %v, want set-1", deleted)
	}
	if !deleted[0].GetForce() {
		t.Error("delete was not forced; a running runner would be refused")
	}
}

// TestDeleteTreatsMissingAsDone checks that an instance that deleted itself on
// exit is what was wanted, not an error.
func TestDeleteTreatsMissingAsDone(t *testing.T) {
	p, d := newTestProvider(t)
	d.missing = true

	if err := p.Delete(t.Context(), "set-1"); err != nil {
		t.Errorf("Delete() = %v, want nil for an instance that is already gone", err)
	}
}

func TestDeleteReportsOtherFailures(t *testing.T) {
	p, d := newTestProvider(t)
	d.err = status.Error(codes.Internal, "disk full")

	err := p.Delete(t.Context(), "set-1")
	if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "set-1") {
		t.Errorf("Delete() = %v, want the daemon's error, naming the instance", err)
	}
}
