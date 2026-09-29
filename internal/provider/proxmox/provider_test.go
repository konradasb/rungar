// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package proxmox

import (
	"context"
	"strings"
	"testing"

	"github.com/konradasb/rungar/internal/types"
)

func TestListKeepsTheSelectorsVMs(t *testing.T) {
	f := newFakePVE(t)
	p := f.open(t)
	ctx := context.Background()

	for _, spec := range []types.MachineSpec{testMachine("rungar-a-1", "a"), testMachine("rungar-b-1", "b")} {
		if err := p.Create(ctx, spec); err != nil {
			t.Fatal(err)
		}
	}

	// A VM whose tags match, but whose description is not Rungar's.
	selector := types.ScaleSetSelector("gh-test", "a")
	f.addVM(&fakeVM{
		resource:    resource{Type: "qemu", Node: "pve1", VMID: 500, Name: "impostor", Status: "running", Tags: strings.Join(tagsOf(selector), ";")},
		description: "not json",
	})

	before := f.count("GET nodes/")
	machines, err := p.List(ctx, selector)
	if err != nil {
		t.Fatalf("List() = %v", err)
	}

	if len(machines) != 1 || machines[0].Name != "rungar-a-1" {
		t.Errorf("List() = %v, want rungar-a-1 alone", machines)
	}

	// The configurations read are of the VMs whose tags matched alone.
	if n := f.count("GET nodes/") - before; n != 2 {
		t.Errorf("read %d configurations, want 2: rungar-a-1's and the impostor's", n)
	}
}

// TestDeleteDestroysWhatIsThereAndToleratesWhatIsNot checks a running VM is
// stopped and destroyed, a half-created one destroyed without being stopped,
// and one already gone is not an error.
func TestDeleteDestroysWhatIsThereAndToleratesWhatIsNot(t *testing.T) {
	f := newFakePVE(t)
	p := f.open(t)
	ctx := context.Background()

	if err := p.Create(ctx, testMachine("rungar-a", "a")); err != nil {
		t.Fatal(err)
	}
	// Half created: cloned, but neither configured nor started.
	f.addVM(&fakeVM{resource: resource{Type: "qemu", Node: "pve2", VMID: 300, Name: "rungar-b", Status: "stopped"}})

	for _, name := range []string{"rungar-a", "rungar-b", "rungar-gone"} {
		if err := p.Delete(ctx, name); err != nil {
			t.Errorf("Delete(%s) = %v", name, err)
		}
		if f.vm(name) != nil {
			t.Errorf("%s is still there", name)
		}
	}

	if n := f.count("POST nodes/pve2/qemu/300/status/stop"); n != 0 {
		t.Errorf("stopped the stopped half-created VM %d times", n)
	}
	f.mu.Lock()
	template := f.vms[9000]
	f.mu.Unlock()
	if template == nil {
		t.Error("the template was deleted")
	}
}
