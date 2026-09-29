// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"

	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// ListProviders returns the providers, in configuration order.
func (s *Server) ListProviders(
	ctx context.Context, _ *rungarv1.ListProvidersRequest,
) (*rungarv1.ListProvidersResponse, error) {
	providers, err := s.scaleSets.Providers(ctx, "")
	if err != nil {
		return nil, err
	}

	out := &rungarv1.ListProvidersResponse{}
	for _, p := range providers {
		out.Providers = append(out.Providers, providerToProto(p))
	}

	return out, nil
}

// GetProvider returns one provider.
func (s *Server) GetProvider(ctx context.Context, req *rungarv1.GetProviderRequest) (*rungarv1.Provider, error) {
	providers, err := s.scaleSets.Providers(ctx, req.GetName())
	if err != nil {
		return nil, err
	}

	// Providers returns an errdefs.ErrNotFound error, not nothing, for a name
	// it does not have. The guard is deliberate defence: a ScaleSetManager
	// that broke that promise would otherwise panic the handler.
	if len(providers) == 0 {
		return nil, errdefs.NotFound("no provider %q", req.GetName())
	}

	return providerToProto(providers[0]), nil
}

// DisableProvider takes a provider out of placement until it is enabled again
// or the daemon restarts.
func (s *Server) DisableProvider(
	ctx context.Context, req *rungarv1.DisableProviderRequest,
) (*rungarv1.Provider, error) {
	return s.setDisabled(ctx, req.GetName(), true)
}

// EnableProvider puts a provider back into placement.
func (s *Server) EnableProvider(
	ctx context.Context, req *rungarv1.EnableProviderRequest,
) (*rungarv1.Provider, error) {
	return s.setDisabled(ctx, req.GetName(), false)
}

// setDisabled disables or enables a provider, and returns it.
func (s *Server) setDisabled(ctx context.Context, name string, disabled bool) (*rungarv1.Provider, error) {
	p, err := s.scaleSets.SetProviderDisabled(ctx, name, disabled)
	if err != nil {
		return nil, err
	}

	return providerToProto(p), nil
}

// providerToProto converts a provider.
func providerToProto(p types.Provider) *rungarv1.Provider {
	snapshot := p.Snapshot

	out := &rungarv1.Provider{
		Name:               snapshot.Name,
		Type:               snapshot.Type,
		Endpoint:           p.Endpoint,
		Weight:             snapshot.Weight,
		Disabled:           snapshot.Disabled,
		ConfiguredDisabled: p.ConfiguredDisabled,
		Reachable:          snapshot.Reachable,
		Error:              snapshot.Error,
		RunnerCount:        int32(snapshot.RunnerCount),
		MaxRunners:         int32(snapshot.MaxRunners),
	}

	if h := snapshot.Hold; h != nil {
		out.Hold = &rungarv1.Hold{
			ScaleSet:  h.ScaleSet,
			Priority:  int32(h.Priority),
			Remaining: durationpb.New(h.Remaining),
		}
	}

	for _, set := range p.ScaleSets {
		out.ScaleSets = append(out.ScaleSets, providerScaleSetToProto(set))
	}

	return out
}

// providerScaleSetToProto converts how a provider stands for one scale set.
func providerScaleSetToProto(set types.ProviderScaleSet) *rungarv1.ProviderScaleSet {
	out := &rungarv1.ProviderScaleSet{
		ScaleSet: set.Name,
		Refusals: int32(set.Refusals),
		Refusal:  set.Refusal,
		Full:     set.Full,
	}
	if set.BackoffFor > 0 {
		out.BackoffFor = durationpb.New(set.BackoffFor)
	}

	return out
}
