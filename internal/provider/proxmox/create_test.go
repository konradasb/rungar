// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package proxmox

import (
	"context"
	"errors"
	"maps"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

func TestCreateBuildsTheVMFromTheRunnerBlock(t *testing.T) {
	f := newFakePVE(t)
	f.agentAfter = 2
	f.polls = 2
	p := f.open(t)
	ctx := context.Background()

	spec := testMachine("rungar-c2-m4-1a2b3c4d", "rungar-c2-m4")
	before := time.Now().Truncate(time.Second)
	if err := p.Create(ctx, spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	vm := f.vm(spec.Name)
	if vm == nil {
		t.Fatal("no VM was created")
	}

	// pve2 has 28 GiB free to pve1's 24.
	if vm.Node != "pve2" {
		t.Errorf("created on %s, want pve2, with the most memory free", vm.Node)
	}
	if vm.cores != 2 || vm.memoryMiB != 4096 || vm.agent != "1" {
		t.Errorf("configured %d cores, %d MiB, agent %q; want 2, 4096, 1", vm.cores, vm.memoryMiB, vm.agent)
	}
	if want := strings.Join(tagsOf(spec.Labels), ";"); vm.Tags != want {
		t.Errorf("tags = %q, want %q, replacing the template's", vm.Tags, want)
	}
	if got := vm.files[defaultJITPath]; got != spec.JITConfig {
		t.Errorf("wrote %q to %s, want the registration", got, defaultJITPath)
	}
	if n := f.count("POST nodes/pve2/qemu/" + strconv.Itoa(vm.VMID) + "/agent/ping"); n != 3 {
		t.Errorf("pinged the agent %d times, want 3: twice before it answered", n)
	}

	machines, err := p.List(ctx, types.ScaleSetSelector("gh-test", "rungar-c2-m4"))
	if err != nil || len(machines) != 1 {
		t.Fatalf("List() = %v, %v; want the runner", machines, err)
	}

	m := machines[0]
	switch {
	case m.Name != spec.Name:
		t.Errorf("Name = %q, want %q", m.Name, spec.Name)
	case !maps.Equal(m.Labels, spec.Labels):
		t.Errorf("Labels = %v, want %v", m.Labels, spec.Labels)
	case m.State != types.MachineRunning:
		t.Errorf("State = %q, want running", m.State)
	case m.Size != "2 vCPU, 4 GiB":
		t.Errorf("Size = %q, want 2 vCPU, 4 GiB", m.Size)
	case m.CreatedAt.Before(before) || m.CreatedAt.After(time.Now()):
		t.Errorf("CreatedAt = %v, want about now", m.CreatedAt)
	}
}

func TestCreateClonesOnTheTemplatesNodeWithoutATarget(t *testing.T) {
	f := newFakePVE(t)
	p := f.open(t, "pve1")

	if err := p.Create(context.Background(), testMachine("rungar-a", "a")); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	if vm := f.vm("rungar-a"); vm == nil || vm.Node != "pve1" {
		t.Fatalf("created %+v, want it on pve1, the one node allowed", vm)
	}
}

func TestCreateSkipsNodesThatCannotTakeTheRunner(t *testing.T) {
	f := newFakePVE(t)
	// pve2 has the most memory free, but is offline; pve3 too few cores.
	f.nodes[1].Status = "offline"
	f.nodes = append(f.nodes, resource{Type: "node", Node: "pve3", Status: "online", MaxCPU: 1, MaxMemory: 64 * gib})
	p := f.open(t)

	if err := p.Create(context.Background(), testMachine("rungar-a", "a")); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	if vm := f.vm("rungar-a"); vm == nil || vm.Node != "pve1" {
		t.Fatalf("created %+v, want it on pve1", vm)
	}
}

func TestCreateRetriesAVMIDTakenMeanwhile(t *testing.T) {
	f := newFakePVE(t)
	f.vms[105] = &fakeVM{resource: resource{Type: "qemu", Node: "pve1", VMID: 105, Name: "other"}}
	f.ids = []int{105, 106}
	p := f.open(t)

	if err := p.Create(context.Background(), testMachine("rungar-a", "a")); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	if vm := f.vm("rungar-a"); vm == nil || vm.VMID != 106 {
		t.Fatalf("created %+v, want VMID 106", vm)
	}
}

func TestCreateSaysWhyItRefused(t *testing.T) {
	tests := []struct {
		name  string
		setup func(f *fakePVE, spec *types.MachineSpec)
		nodes []string
		class error // nil: neither class
	}{
		{
			name:  "no node has the memory free",
			setup: func(f *fakePVE, _ *types.MachineSpec) { f.nodes[0].Memory, f.nodes[1].Memory = 63*gib, 30*gib },
			class: errdefs.ErrNoCapacity,
		},
		{
			name: "more memory than any node has",
			setup: func(_ *fakePVE, spec *types.MachineSpec) {
				r := testRunner()
				r.Memory = types.Size(128 * gib)
				spec.Runner = r
			},
			class: errdefs.ErrInvalidArgument,
		},
		{
			name: "more cores than any node has",
			setup: func(_ *fakePVE, spec *types.MachineSpec) {
				r := testRunner()
				r.Cores = 32
				spec.Runner = r
			},
			class: errdefs.ErrInvalidArgument,
		},
		{
			name: "no such template",
			setup: func(_ *fakePVE, spec *types.MachineSpec) {
				r := testRunner()
				r.Template = 9999
				spec.Runner = r
			},
			class: errdefs.ErrInvalidArgument,
		},
		{
			name:  "the template is not a template",
			setup: func(f *fakePVE, _ *types.MachineSpec) { f.vms[9000].Template = 0 },
			class: errdefs.ErrInvalidArgument,
		},
		{
			name:  "nodes the cluster does not have",
			nodes: []string{"pve9"},
			class: errdefs.ErrInvalidArgument,
		},
		{
			name:  "another provider's runner",
			setup: func(_ *fakePVE, spec *types.MachineSpec) { spec.Runner = otherRunner{} },
			class: errdefs.ErrInvalidArgument,
		},
		{
			name: "a clone out of disk",
			setup: func(f *fakePVE, _ *types.MachineSpec) {
				f.exits["qmclone"] = "clone failed: write error: No space left on device"
			},
			class: errdefs.ErrNoCapacity,
		},
		{
			name: "a start out of memory",
			setup: func(f *fakePVE, _ *types.MachineSpec) {
				f.exits["qmstart"] = "start failed: QEMU exited with code 1: kvm: cannot allocate memory"
			},
			class: errdefs.ErrNoCapacity,
		},
		{
			name: "a template on storage the node cannot reach",
			setup: func(f *fakePVE, _ *types.MachineSpec) {
				f.exits["qmclone"] = "can't clone VM to node 'pve2' (VM uses local storage)"
			},
			class: errdefs.ErrInvalidArgument,
		},
		{
			name:  "a clone that fails otherwise",
			setup: func(f *fakePVE, _ *types.MachineSpec) { f.exits["qmclone"] = "clone failed: storage is locked" },
		},
		{
			name: "no node online",
			setup: func(f *fakePVE, _ *types.MachineSpec) {
				f.nodes[0].Status, f.nodes[1].Status = "offline", "offline"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakePVE(t)
			spec := testMachine("rungar-a", "a")
			if tt.setup != nil {
				tt.setup(f, &spec)
			}
			p := f.open(t, tt.nodes...)

			err := p.Create(context.Background(), spec)
			switch {
			case err == nil:
				t.Fatal("Create() = nil, want a refusal")
			case tt.class != nil && !errors.Is(err, tt.class):
				t.Errorf("Create() = %v, want %v", err, tt.class)
			case tt.class == nil && (errors.Is(err, errdefs.ErrNoCapacity) || errors.Is(err, errdefs.ErrInvalidArgument)):
				t.Errorf("Create() = %v, want neither no capacity nor invalid", err)
			}
		})
	}
}

func TestCreateGivesUpOnAnAgentThatNeverAnswers(t *testing.T) {
	f := newFakePVE(t)
	f.agentAfter = 1 << 30
	p := f.open(t)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := p.Create(ctx, testMachine("rungar-a", "a"))
	if err == nil || !strings.Contains(err.Error(), "guest agent did not answer") {
		t.Fatalf("Create() = %v, want the agent not answering", err)
	}
	if errors.Is(err, errdefs.ErrNoCapacity) || errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("Create() = %v, want neither no capacity nor invalid", err)
	}

	// Left for the fleet's Delete, as a failed Create is.
	if vm := f.vm("rungar-a"); vm == nil {
		t.Fatal("no VM left to delete")
	}
	if err := p.Delete(context.Background(), "rungar-a"); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if vm := f.vm("rungar-a"); vm != nil {
		t.Errorf("VM %d is still there", vm.VMID)
	}
}
