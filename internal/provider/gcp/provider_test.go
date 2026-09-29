// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package gcp

import (
	"context"
	"maps"
	"net/http"
	"testing"
	"time"

	"google.golang.org/api/compute/v1"

	"github.com/konradasb/rungar/internal/types"
)

func TestListReturnsTheScaleSetsMachines(t *testing.T) {
	f := newFakeCompute()
	f.failures["europe-west1-b"] = failure{code: "ZONE_RESOURCE_POOL_EXHAUSTED"}
	p := newTestProvider(t, testConfig(), f)
	ctx := context.Background()

	mine := testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8")
	other := testMachine("rungar-c2-m4-5e6f7a8b", "rungar-c2-m4")
	for _, spec := range []types.MachineSpec{mine, other} {
		if err := p.Create(ctx, spec); err != nil {
			t.Fatalf("Create(%s) = %v", spec.Name, err)
		}
	}

	// An instance of somebody else's, labelled the same, but not by Rungar.
	f.mu.Lock()
	f.instances["europe-west1-d"] = map[string]*compute.Instance{"stranger": {
		Name:     "stranger",
		Labels:   computeLabels(mine.Labels),
		Metadata: &compute.Metadata{},
	}}
	f.mu.Unlock()

	selector := types.ScaleSetSelector("gh-4e3651f01be5", "rungar-c4-m8")
	got, err := p.List(ctx, selector)
	if err != nil {
		t.Fatalf("List() = %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("List() = %+v, want the scale set's one machine", got)
	}

	m := got[0]
	want := types.Machine{
		Name:      mine.Name,
		Labels:    mine.Labels,
		State:     types.MachineStarting,
		Size:      "e2-standard-4",
		CreatedAt: time.Date(2026, 9, 29, 17, 0, 0, 0, time.UTC),
	}
	if m.Name != want.Name || !maps.Equal(m.Labels, want.Labels) || m.State != want.State ||
		m.Size != want.Size || !m.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("List()[0] = %+v, want %+v", m, want)
	}

	f.mu.Lock()
	filters := f.filters
	f.mu.Unlock()
	if last := filters[len(filters)-1]; last != labelFilter(selector) {
		t.Errorf("filter = %q, want %q", last, labelFilter(selector))
	}
}

func TestListFailsWhenAZoneCannotBeListed(t *testing.T) {
	f := newFakeCompute()
	f.listFailures["europe-west1-c"] = http.StatusInternalServerError
	p := newTestProvider(t, testConfig(), f)

	if _, err := p.List(context.Background(), map[string]string{types.LabelManaged: "true"}); err == nil {
		t.Error("List() = nil error, want one: what europe-west1-c has is unknown")
	}
}

// TestDeleteFindsTheInstanceInAnyZone checks an instance is deleted from the
// zone it is in, and one already gone is not an error.
func TestDeleteFindsTheInstanceInAnyZone(t *testing.T) {
	f := newFakeCompute()
	f.failures["europe-west1-b"] = failure{code: "ZONE_RESOURCE_POOL_EXHAUSTED"}
	p := newTestProvider(t, testConfig(), f)
	ctx := context.Background()

	spec := testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8")
	if err := p.Create(ctx, spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	if err := p.Delete(ctx, spec.Name); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if f.instance("europe-west1-c", spec.Name) != nil {
		t.Error("the instance is still in europe-west1-c")
	}

	deletes := f.count(http.MethodDelete, instancePath)
	if err := p.Delete(ctx, spec.Name); err != nil {
		t.Errorf("Delete() of an instance already gone = %v, want nil", err)
	}
	if n := f.count(http.MethodDelete, instancePath); n != deletes {
		t.Errorf("deleted %d more times an instance no zone has", n-deletes)
	}
}

// TestDeleteOfAnInstanceThatGoesMeanwhile checks an instance Compute Engine
// deletes while Rungar is deleting it, as a Spot instance taken back can be,
// is deleted, not failed to be.
func TestDeleteOfAnInstanceThatGoesMeanwhile(t *testing.T) {
	f := newFakeCompute()
	p := newTestProvider(t, testConfig(), f)
	ctx := context.Background()

	spec := testMachine("rungar-spot-1", "rungar-spot")
	if err := p.Create(ctx, spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	f.mu.Lock()
	f.vanishing[spec.Name] = true
	f.mu.Unlock()

	if err := p.Delete(ctx, spec.Name); err != nil {
		t.Errorf("Delete() = %v, want nil: the instance is gone", err)
	}
}

// TestDeleteWhenTheOperationIsUnknown checks a deletion whose operation
// Compute Engine says it does not know is not taken for the instance being
// gone: how the deletion ended is unknown.
func TestDeleteWhenTheOperationIsUnknown(t *testing.T) {
	f := newFakeCompute()
	p := newTestProvider(t, testConfig(), f)
	ctx := context.Background()

	spec := testMachine("rungar-x-1", "rungar-x")
	if err := p.Create(ctx, spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	// A vanishing instance's deletion is answered running, so it is waited
	// for.
	f.mu.Lock()
	f.vanishing[spec.Name] = true
	f.waitFailures["europe-west1-b"] = []int{http.StatusNotFound}
	f.mu.Unlock()

	if err := p.Delete(ctx, spec.Name); err == nil {
		t.Error("Delete() = nil, want an error: the operation's end is unknown")
	}
}
