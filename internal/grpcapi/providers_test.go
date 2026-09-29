// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/konradasb/rungar/internal/types"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

func TestListProviders(t *testing.T) {
	f := newFixture(t, "rungar-vm/rungar-vm-1", "gone-vm/gone-vm-1")

	resp, err := f.client.ListProviders(context.Background(), &rungarv1.ListProvidersRequest{})
	if err != nil {
		t.Fatal(err)
	}

	if len(resp.GetProviders()) != 1 {
		t.Fatalf("got %d providers, want 1", len(resp.GetProviders()))
	}

	p := resp.GetProviders()[0]
	switch {
	case p.GetName() != "compute1" || p.GetType() != "dicer" || p.GetEndpoint() != "10.0.0.1:7443":
		t.Errorf("provider = %s %s %s, want compute1 dicer 10.0.0.1:7443", p.GetName(), p.GetType(), p.GetEndpoint())
	case !p.GetReachable() || p.GetDisabled():
		t.Errorf("reachable = %v, disabled = %v; want reachable and enabled", p.GetReachable(), p.GetDisabled())
	case p.GetRunnerCount() != 2:
		t.Errorf("runners = %d, want every scale set's: 2", p.GetRunnerCount())
	case len(p.GetPlacements()) != 1 || p.GetPlacements()[0].GetScaleSet() != "rungar-vm":
		t.Errorf("placements = %v, want rungar-vm's", p.GetPlacements())
	case len(p.GetScaleSets()) != 1 || p.GetScaleSets()[0] != "rungar-vm":
		t.Errorf("scale sets = %v, want [rungar-vm]", p.GetScaleSets())
	}
}

func TestGetProvider(t *testing.T) {
	f := newFixture(t)

	p, err := f.client.GetProvider(context.Background(), &rungarv1.GetProviderRequest{Name: "compute1"})
	if err != nil {
		t.Fatal(err)
	}
	if p.GetName() != "compute1" {
		t.Errorf("name = %q, want compute1", p.GetName())
	}

	_, err = f.client.GetProvider(context.Background(), &rungarv1.GetProviderRequest{Name: "nowhere"})
	wantCode(t, err, codes.NotFound)
}

// TestDisableAndEnableProvider checks a provider is taken out of placement
// and put back, and that each says it differs from the configuration only
// while it does.
func TestDisableAndEnableProvider(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	p, err := f.client.DisableProvider(ctx, &rungarv1.DisableProviderRequest{Name: "compute1"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.GetDisabled() || p.GetConfiguredDisabled() {
		t.Errorf("after disabling: disabled = %v, configured disabled = %v; want true, false",
			p.GetDisabled(), p.GetConfiguredDisabled())
	}

	p, err = f.client.EnableProvider(ctx, &rungarv1.EnableProviderRequest{Name: "compute1"})
	if err != nil {
		t.Fatal(err)
	}
	if p.GetDisabled() {
		t.Error("after enabling: still disabled")
	}

	_, err = f.client.DisableProvider(ctx, &rungarv1.DisableProviderRequest{Name: "nowhere"})
	wantCode(t, err, codes.NotFound)

	_, err = f.client.EnableProvider(ctx, &rungarv1.EnableProviderRequest{Name: "nowhere"})
	wantCode(t, err, codes.NotFound)
}

func TestProviderToProto(t *testing.T) {
	out := providerToProto(types.Provider{
		Snapshot: types.ProviderSnapshot{
			Name: "compute1", Type: "dicer", Weight: 2, Disabled: true, Reachable: true,
			RunnerCount: 5, MaxRunners: 8,
			Hold: &types.Hold{ScaleSet: "big-vm", Priority: 9, Remaining: 30 * time.Second},
		},
		Endpoint: "10.0.0.1:7443",
		ScaleSets: []types.ProviderScaleSet{
			{Name: "rungar-vm"},
			{Name: "big-vm", BackoffFor: 9 * time.Second, Failures: 1, Failure: "not enough memory", Full: true},
			{Name: "gpu-vm", BackoffFor: time.Minute, Failures: 3, Failure: "connection reset"},
		},
	})

	switch {
	case out.GetName() != "compute1" || out.GetType() != "dicer" || out.GetWeight() != 2 || !out.GetDisabled():
		t.Errorf("provider = %v", out)
	case out.GetRunnerCount() != 5 || out.GetMaxRunners() != 8:
		t.Errorf("runners %d of %d, want 5 of 8", out.GetRunnerCount(), out.GetMaxRunners())
	case out.GetHold().GetScaleSet() != "big-vm" || out.GetHold().GetRemaining().AsDuration() != 30*time.Second:
		t.Errorf("hold = %v", out.GetHold())
	}

	if got := out.GetScaleSets(); len(got) != 3 || got[0] != "rungar-vm" || got[2] != "gpu-vm" {
		t.Errorf("scale sets = %v, want them in order", got)
	}

	pls := out.GetPlacements()
	if len(pls) != 3 {
		t.Fatalf("placements = %v, want one per scale set", pls)
	}
	switch {
	case pls[0].GetScaleSet() != "rungar-vm" || pls[0].GetBackoffFor() != nil || pls[0].GetFailure() != "":
		t.Errorf("rungar-vm = %v, want it tried", pls[0])
	case pls[1].GetBackoffFor().AsDuration() != 9*time.Second || !pls[1].GetFull() ||
		pls[1].GetFailures() != 1 || pls[1].GetFailure() != "not enough memory":
		t.Errorf("big-vm = %v, want it full for 9s", pls[1])
	case pls[2].GetBackoffFor().AsDuration() != time.Minute || pls[2].GetFull() ||
		pls[2].GetFailures() != 3 || pls[2].GetFailure() != "connection reset":
		t.Errorf("gpu-vm = %v, want it failing for 1m after 3 failures, saying why", pls[2])
	}

	bare := providerToProto(types.Provider{Snapshot: types.ProviderSnapshot{Name: "cloud", Err: "refused"}})

	switch {
	case bare.GetHold() != nil:
		t.Errorf("a provider that says nothing = %v, want no hold", bare)
	case bare.GetPlacements() != nil || bare.GetMaxRunners() != 0:
		t.Errorf("placements %v, limit %d; want none", bare.GetPlacements(), bare.GetMaxRunners())
	case bare.GetError() != "refused":
		t.Errorf("error = %q, want refused", bare.GetError())
	}
}
