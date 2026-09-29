// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// compute1 is a provider as the scale sets report it.
func compute1() types.Provider {
	return types.Provider{
		Snapshot: types.ProviderSnapshot{
			Name: "compute1", Type: "dicer", Weight: 1, Reachable: true, RunnerCount: 2,
		},
		Endpoint:  "10.0.0.1:7443",
		ScaleSets: []types.ProviderScaleSet{{Name: "rungar-vm"}},
	}
}

func TestListProvidersSendsEveryProviderInOrder(t *testing.T) {
	f := newFixture(t)
	cloud := types.Provider{Snapshot: types.ProviderSnapshot{Name: "cloud", Type: "gcp"}}
	f.scaleSets.providers = []types.Provider{compute1(), cloud}

	resp, err := f.client.ListProviders(context.Background(), &rungarv1.ListProvidersRequest{})
	if err != nil {
		t.Fatal(err)
	}

	providers := resp.GetProviders()
	if len(providers) != 2 || providers[0].GetName() != "compute1" || providers[1].GetName() != "cloud" {
		t.Fatalf("providers = %v, want compute1 then cloud", providers)
	}

	p := providers[0]
	switch {
	case p.GetType() != "dicer" || p.GetEndpoint() != "10.0.0.1:7443":
		t.Errorf("provider = %s %s, want dicer 10.0.0.1:7443", p.GetType(), p.GetEndpoint())
	case !p.GetReachable() || p.GetDisabled():
		t.Errorf("reachable = %v, disabled = %v; want reachable and enabled", p.GetReachable(), p.GetDisabled())
	case p.GetRunnerCount() != 2:
		t.Errorf("runners = %d, want 2", p.GetRunnerCount())
	case len(p.GetScaleSets()) != 1 || p.GetScaleSets()[0].GetScaleSet() != "rungar-vm":
		t.Errorf("scale sets = %v, want [rungar-vm]", p.GetScaleSets())
	}
}

func TestGetProviderFindsByName(t *testing.T) {
	f := newFixture(t)
	f.scaleSets.providers = []types.Provider{compute1()}

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

// emptyProviders answers every provider lookup with nothing at all.
type emptyProviders struct{ fakeScaleSets }

func (*emptyProviders) Providers(context.Context, string) ([]types.Provider, error) { return nil, nil }

// TestGetProviderWithNothingFound checks a lookup that finds nothing is
// NotFound rather than a crash.
func TestGetProviderWithNothingFound(t *testing.T) {
	s := NewServer(Config{ScaleSets: &emptyProviders{}})

	_, err := s.GetProvider(context.Background(), &rungarv1.GetProviderRequest{Name: "compute1"})
	if !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("GetProvider() = %v, want an ErrNotFound", err)
	}
}

// TestDisableAndEnableProvider checks a provider is disabled and enabled by
// name, and each answers with the provider as it then is.
func TestDisableAndEnableProvider(t *testing.T) {
	f := newFixture(t)
	f.scaleSets.providers = []types.Provider{compute1()}
	ctx := context.Background()

	p, err := f.client.DisableProvider(ctx, &rungarv1.DisableProviderRequest{Name: "compute1"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.GetDisabled() {
		t.Error("after disabling: not disabled")
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

func TestProviderToProtoCarriesEveryField(t *testing.T) {
	out := providerToProto(types.Provider{
		Snapshot: types.ProviderSnapshot{
			Name: "compute1", Type: "dicer", Weight: 2, Disabled: true, Reachable: true,
			RunnerCount: 5, MaxRunners: 8,
			Hold: &types.Hold{ScaleSet: "big-vm", Priority: 9, Remaining: 30 * time.Second},
		},
		Endpoint:           "10.0.0.1:7443",
		ConfiguredDisabled: true,
		ScaleSets: []types.ProviderScaleSet{
			{Name: "rungar-vm"},
			{Name: "big-vm", BackoffFor: 9 * time.Second, Refusals: 1, Refusal: "not enough memory", Full: true},
			{Name: "gpu-vm", BackoffFor: time.Minute, Refusals: 3, Refusal: "connection reset"},
		},
	})

	switch {
	case out.GetName() != "compute1" || out.GetType() != "dicer" || out.GetWeight() != 2 || !out.GetDisabled():
		t.Errorf("provider = %v", out)
	case out.GetEndpoint() != "10.0.0.1:7443" || !out.GetConfiguredDisabled() || !out.GetReachable():
		t.Errorf("endpoint %q, configured disabled %v, reachable %v; want 10.0.0.1:7443, true, true",
			out.GetEndpoint(), out.GetConfiguredDisabled(), out.GetReachable())
	case out.GetRunnerCount() != 5 || out.GetMaxRunners() != 8:
		t.Errorf("runners %d of %d, want 5 of 8", out.GetRunnerCount(), out.GetMaxRunners())
	case out.GetHold().GetScaleSet() != "big-vm" || out.GetHold().GetPriority() != 9 ||
		out.GetHold().GetRemaining().AsDuration() != 30*time.Second:
		t.Errorf("hold = %v", out.GetHold())
	}

	sets := out.GetScaleSets()
	if len(sets) != 3 {
		t.Fatalf("scale sets = %v, want one per scale set placed on it", sets)
	}
	switch {
	case sets[0].GetScaleSet() != "rungar-vm" || sets[0].GetBackoffFor() != nil || sets[0].GetRefusal() != "":
		t.Errorf("rungar-vm = %v, want it tried", sets[0])
	case sets[1].GetBackoffFor().AsDuration() != 9*time.Second || !sets[1].GetFull() ||
		sets[1].GetRefusals() != 1 || sets[1].GetRefusal() != "not enough memory":
		t.Errorf("big-vm = %v, want it full for 9s", sets[1])
	case sets[2].GetScaleSet() != "gpu-vm" || sets[2].GetBackoffFor().AsDuration() != time.Minute || sets[2].GetFull() ||
		sets[2].GetRefusals() != 3 || sets[2].GetRefusal() != "connection reset":
		t.Errorf("gpu-vm = %v, want it backed off for 1m after 3 refusals, saying why", sets[2])
	}

	bare := providerToProto(types.Provider{Snapshot: types.ProviderSnapshot{Name: "cloud", Error: "refused"}})

	switch {
	case bare.GetHold() != nil:
		t.Errorf("a provider that says nothing = %v, want no hold", bare)
	case bare.GetScaleSets() != nil || bare.GetMaxRunners() != 0:
		t.Errorf("scale sets %v, limit %d; want none", bare.GetScaleSets(), bare.GetMaxRunners())
	case bare.GetError() != "refused":
		t.Errorf("error = %q, want refused", bare.GetError())
	}
}
