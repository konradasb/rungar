// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package dicer implements the dicer provider type: runners as virtual
// machines on a Dicer host. Dicer runs on one machine, so each Dicer host is
// a provider of its own.
package dicer

import (
	"context"
	"fmt"
	"log/slog"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

// dicerAPI is the part of the Dicer API the provider uses.
type dicerAPI interface {
	ListInstances(ctx context.Context, in *dicerdv1.ListInstancesRequest,
		opts ...grpc.CallOption) (*dicerdv1.ListInstancesResponse, error)
	CreateInstance(ctx context.Context, in *dicerdv1.CreateInstanceRequest,
		opts ...grpc.CallOption) (*dicerdv1.Instance, error)
	DeleteInstance(ctx context.Context, in *dicerdv1.DeleteInstanceRequest,
		opts ...grpc.CallOption) (*emptypb.Empty, error)
	GetImage(ctx context.Context, in *dicerdv1.GetImageRequest,
		opts ...grpc.CallOption) (*dicerdv1.Image, error)
	PullImage(ctx context.Context, in *dicerdv1.PullImageRequest,
		opts ...grpc.CallOption) (grpc.ServerStreamingClient[dicerdv1.PullImageProgress], error)
	Close() error
}

// Provider is a connected Dicer daemon. Its gRPC connection reconnects on its
// own to a daemon that goes away and comes back. It is safe for concurrent
// use.
type Provider struct {
	config *Config
	client dicerAPI
	logger *slog.Logger
}

var _ provider.Provider = (*Provider)(nil)

// List returns the instances carrying the selector's labels.
func (p *Provider) List(ctx context.Context, selector map[string]string) ([]types.Machine, error) {
	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	resp, err := p.client.ListInstances(ctx, &dicerdv1.ListInstancesRequest{})
	if err != nil {
		return nil, fmt.Errorf("list instances: %w", err)
	}

	var out []types.Machine
	for _, instance := range resp.GetInstances() {
		if types.HasLabels(instance.GetLabels(), selector) {
			out = append(out, machineOf(instance))
		}
	}

	return out, nil
}

// Delete deletes an instance, stopping it first. One not there is not an
// error.
func (p *Provider) Delete(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	_, err := p.client.DeleteInstance(ctx, &dicerdv1.DeleteInstanceRequest{Name: name, Force: true})
	if err != nil && status.Code(err) != codes.NotFound {
		return fmt.Errorf("delete instance %s: %w", name, err)
	}

	return nil
}

// Close closes the connection to the daemon.
func (p *Provider) Close() error {
	return p.client.Close()
}
