// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package dicer implements the dicer provider type: runners as virtual
// machines on a Dicer host. Dicer runs on one machine, so each Dicer host is
// a provider of its own.
package dicer

import (
	"context"
	"log/slog"
	"time"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

// client is the part of the Dicer API the provider uses.
type client interface {
	ListInstances(ctx context.Context, in *dicerdv1.ListInstancesRequest,
		opts ...grpc.CallOption) (*dicerdv1.ListInstancesResponse, error)
	CreateInstance(ctx context.Context, in *dicerdv1.CreateInstanceRequest,
		opts ...grpc.CallOption) (*dicerdv1.Instance, error)
	DeleteInstance(ctx context.Context, in *dicerdv1.DeleteInstanceRequest,
		opts ...grpc.CallOption) (*emptypb.Empty, error)
	Close() error
}

// Provider is a connected Dicer daemon. Its gRPC connection reconnects on its
// own to a daemon that goes away and comes back.
type Provider struct {
	client  client
	timeout time.Duration
	logger  *slog.Logger
}

var _ provider.Provider = (*Provider)(nil)

// List returns the instances carrying the selector's labels.
func (p *Provider) List(ctx context.Context, selector map[string]string) ([]types.Machine, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	resp, err := p.client.ListInstances(ctx, &dicerdv1.ListInstancesRequest{})
	if err != nil {
		return nil, err
	}

	var out []types.Machine
	for _, inst := range resp.GetInstances() {
		if types.Matches(inst.GetLabels(), selector) {
			out = append(out, machineOf(inst))
		}
	}

	return out, nil
}

// Create defines a runner's instance and starts it. It is bounded by ctx
// alone, since pulling the image onto a cold host takes as long as it takes.
func (p *Provider) Create(ctx context.Context, spec types.MachineSpec) error {
	runner, ok := spec.Runner.(RunnerSpec)
	if !ok {
		return errdefs.InvalidArgument("runner %q: not a dicer runner (%T)", spec.Name, spec.Runner)
	}

	if _, err := p.client.CreateInstance(ctx, instanceRequest(spec, runner)); err != nil {
		return createError(err)
	}

	return nil
}

// createError classifies the daemon's refusal of an instance as
// provider.Provider.Create requires.
func createError(err error) error {
	message := status.Convert(err).Message()

	switch status.Code(err) {
	case codes.ResourceExhausted:
		// Out of CPU, memory or disk.
		return errdefs.NoCapacity("%s", message)
	case codes.FailedPrecondition:
		// A volume or port the runner needs is another instance's, until
		// that one has gone.
		return errdefs.NoCapacity("%s", message)
	case codes.InvalidArgument, codes.NotFound:
		// More than the host has, or a kernel, network, volume or image it
		// lacks.
		return errdefs.InvalidArgument("%s", message)
	default:
		return err
	}
}

// Delete removes an instance, stopping it first. One not there is not an
// error.
func (p *Provider) Delete(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	_, err := p.client.DeleteInstance(ctx, &dicerdv1.DeleteInstanceRequest{Name: name, Force: true})
	if err != nil && status.Code(err) != codes.NotFound {
		return err
	}

	return nil
}

// Close closes the connection to the daemon.
func (p *Provider) Close() error {
	return p.client.Close()
}
