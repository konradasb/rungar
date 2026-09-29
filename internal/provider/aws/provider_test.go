// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package aws

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/konradasb/rungar/internal/types"
)

func TestListReturnsTheScaleSetsMachines(t *testing.T) {
	f := newFakeEC2(t)
	config := testConfig()
	f.failures[config.Subnets[0]] = "InsufficientInstanceCapacity"
	p := newTestProvider(config, f)
	ctx := t.Context()

	mine := testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8")
	gone := testMachine("rungar-c4-m8-5e6f7a8b", "rungar-c4-m8")
	other := testMachine("rungar-c2-m4-5e6f7a8b", "rungar-c2-m4")
	for _, spec := range []types.MachineSpec{mine, gone, other} {
		if err := p.Create(ctx, spec); err != nil {
			t.Fatalf("Create(%s) = %v", spec.Name, err)
		}
	}
	if err := p.Delete(ctx, gone.Name); err != nil {
		t.Fatalf("Delete() = %v", err)
	}

	// An instance of the scale set's, in a subnet not the provider's.
	f.mu.Lock()
	stray := *f.instances[0]
	stray.InstanceId = aws.String("i-stray")
	stray.SubnetId = aws.String("subnet-0ffffffffffffffff")
	f.instances = append(f.instances, &stray)
	f.mu.Unlock()

	selector := types.ScaleSetSelector("gh-4e3651f01be5", "rungar-c4-m8")
	got, err := p.List(ctx, selector)
	if err != nil {
		t.Fatalf("List() = %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("List() = %+v, want the scale set's one live machine", got)
	}

	m := got[0]
	want := types.Machine{
		Name:      mine.Name,
		Labels:    mine.Labels,
		State:     types.MachineStarting,
		Size:      "m7i.xlarge",
		CreatedAt: time.Date(2026, 9, 29, 17, 0, 0, 0, time.UTC),
	}
	if m.Name != want.Name || !maps.Equal(m.Labels, want.Labels) || m.State != want.State ||
		m.Size != want.Size || !m.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("List()[0] = %+v, want %+v", m, want)
	}
}

func TestListFailsWhenEC2Does(t *testing.T) {
	f := newFakeEC2(t)
	f.describeFailure = "RequestLimitExceeded"
	p := newTestProvider(testConfig(), f)

	_, err := p.List(t.Context(), map[string]string{types.LabelManaged: "true"})
	if err == nil {
		t.Fatal("List() = nil error, want one: what the region has is unknown")
	}
	if refusalOf(err) != refusalOther {
		t.Errorf("List() = %v, want EC2's error wrapped", err)
	}
}

func TestDeleteFailsWhenEC2Does(t *testing.T) {
	f := newFakeEC2(t)
	f.describeFailure = "RequestLimitExceeded"
	p := newTestProvider(testConfig(), f)

	err := p.Delete(t.Context(), "rungar-x-1")
	if err == nil {
		t.Fatal("Delete() = nil error, want one: whether the instance is there is unknown")
	}
	if refusalOf(err) != refusalOther || !strings.Contains(err.Error(), "rungar-x-1") {
		t.Errorf("Delete() = %v, want EC2's error wrapped, naming the instance", err)
	}
}

// TestDeleteTerminatesTheInstance checks Delete terminates the runner's
// instance once, and that deleting it again is not an error.
func TestDeleteTerminatesTheInstance(t *testing.T) {
	f := newFakeEC2(t)
	p := newTestProvider(testConfig(), f)
	ctx := t.Context()

	spec := testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8")
	if err := p.Create(ctx, spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	if err := p.Delete(ctx, spec.Name); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if got := f.named(spec.Name).State.Name; got != ec2types.InstanceStateNameShuttingDown {
		t.Errorf("instance is %s, want shutting-down", got)
	}

	if err := p.Delete(ctx, spec.Name); err != nil {
		t.Errorf("Delete() of an instance already gone = %v, want nil", err)
	}
	terminated := f.terminateRequests()
	if n := len(terminated); n != 1 {
		t.Fatalf("terminated %d times, want once", n)
	}
	if got := terminated[0]; !slices.Equal(got, []string{aws.ToString(f.named(spec.Name).InstanceId)}) {
		t.Errorf("terminated %v, want the instance", got)
	}
}

func TestDeleteLeavesOthersInstances(t *testing.T) {
	f := newFakeEC2(t)
	p := newTestProvider(testConfig(), f)

	// An instance of the same name that is not Rungar's.
	f.mu.Lock()
	f.instances = append(f.instances, &ec2types.Instance{
		InstanceId: aws.String("i-stranger"),
		SubnetId:   aws.String(testConfig().Subnets[0]),
		State:      &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning},
		Tags:       []ec2types.Tag{{Key: aws.String(nameTag), Value: aws.String("rungar-x-1")}},
	})
	f.mu.Unlock()

	if err := p.Delete(t.Context(), "rungar-x-1"); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if got := f.terminateRequests(); len(got) != 0 {
		t.Errorf("terminated %v, want nothing", got)
	}
}
