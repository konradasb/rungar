// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"

	"google.golang.org/protobuf/types/known/durationpb"

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
	state := p.Snapshot

	out := &rungarv1.Provider{
		Name:               state.Name,
		Type:               state.Type,
		Endpoint:           p.Endpoint,
		Weight:             state.Weight,
		Disabled:           state.Disabled,
		ConfiguredDisabled: p.ConfiguredDisabled,
		Reachable:          state.Reachable,
		Error:              state.Err,
		RunnerCount:        int32(state.RunnerCount),
		MaxRunners:         int32(state.MaxRunners),
	}

	if h := state.Hold; h != nil {
		out.Hold = &rungarv1.Hold{
			ScaleSet:  h.ScaleSet,
			Priority:  int32(h.Priority),
			Remaining: durationpb.New(h.Remaining),
		}
	}

	for _, set := range p.ScaleSets {
		out.ScaleSets = append(out.ScaleSets, set.Name)
		out.Placements = append(out.Placements, placementToProto(set))
	}

	return out
}

// placementToProto converts how a provider stands for one scale set.
func placementToProto(set types.ProviderScaleSet) *rungarv1.ProviderScaleSet {
	out := &rungarv1.ProviderScaleSet{
		ScaleSet: set.Name,
		Failures: int32(set.Failures),
		Failure:  set.Failure,
		Full:     set.Full,
	}
	if set.BackoffFor > 0 {
		out.BackoffFor = durationpb.New(set.BackoffFor)
	}

	return out
}
