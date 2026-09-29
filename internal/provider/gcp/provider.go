// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package gcp implements the gcp provider type: runners as Compute Engine
// instances in one Google Cloud project and region.
package gcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

// Provider is a connected Google Cloud project and region.
type Provider struct {
	config  *Config
	compute *compute.Service
	logger  *slog.Logger
}

var _ provider.Provider = (*Provider)(nil)

// operationDone is the status of a finished operation.
const operationDone = "DONE"

// deleteTimeout bounds removing an instance, which takes longer than a call:
// Compute Engine stops it first.
const deleteTimeout = 5 * time.Minute

// List returns the instances in the provider's zones carrying the selector's
// labels.
func (p *Provider) List(ctx context.Context, selector map[string]string) ([]types.Machine, error) {
	instances, err := p.find(ctx, labelFilter(selector))
	if err != nil {
		return nil, err
	}

	var out []types.Machine
	for _, zi := range instances {
		m, ok := machineOf(zi.instance)
		if ok && types.Matches(m.Labels, selector) {
			out = append(out, m)
		}
	}

	return out, nil
}

// Create makes a runner's instance in the first of the provider's zones that
// has it. A zone out of stock or out of quota sends it to the next, after
// removing whatever the zone made of it; when every zone is, it returns an
// errdefs.NoCapacity error. It is bounded by its context alone, since an
// instance takes as long as it takes to be made.
func (p *Provider) Create(ctx context.Context, spec types.MachineSpec) error {
	runner, ok := spec.Runner.(RunnerSpec)
	if !ok {
		return errdefs.InvalidArgument("runner %q: not a gcp runner (%T)", spec.Name, spec.Runner)
	}

	var full []string
	for _, zone := range p.config.Zones {
		err := p.createIn(ctx, zone, spec, runner)
		if err == nil {
			return nil
		}

		switch classify(err) {
		case refusalFull:
			full = append(full, fmt.Sprintf("%s: %v", zone, err))
			if cleanupErr := p.deleteIn(ctx, zone, spec.Name); cleanupErr != nil {
				return fmt.Errorf("%s: %s; and the instance it may have left could not be removed: %w",
					zone, errorMessage(err), cleanupErr)
			}
		case refusalInvalid:
			return errdefs.InvalidArgument("%s", errorMessage(err))
		default:
			return fmt.Errorf("%s: %w", zone, err)
		}
	}

	return errdefs.NoCapacity("%s", strings.Join(full, "; "))
}

// createIn makes a runner's instance in a zone, and waits until it is made.
func (p *Provider) createIn(ctx context.Context, zone string, spec types.MachineSpec, runner RunnerSpec) error {
	inst, err := p.config.instanceFor(spec, runner, zone)
	if err != nil {
		return err
	}

	op, err := p.compute.Instances.Insert(p.config.Project, zone, inst).Context(ctx).Do()
	if err != nil {
		return err
	}

	return p.wait(ctx, zone, op)
}

// Delete removes an instance from whichever of the provider's zones it is in,
// waiting up to deleteTimeout for it to go. One not there is not an error.
func (p *Provider) Delete(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, deleteTimeout)
	defer cancel()

	instances, err := p.find(ctx, nameFilter(name))
	if err != nil {
		return err
	}

	for _, zi := range instances {
		if err := p.deleteIn(ctx, zi.zone, name); err != nil {
			return err
		}
	}

	return nil
}

// deleteIn removes an instance from a zone, and waits until it is gone. One
// not there is not an error.
func (p *Provider) deleteIn(ctx context.Context, zone, name string) error {
	op, err := p.compute.Instances.Delete(p.config.Project, zone, name).Context(ctx).Do()
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	return p.wait(ctx, zone, op)
}

// Close does nothing: the client holds no connection of its own.
func (p *Provider) Close() error {
	return nil
}

// zonedInstance is an instance and the zone it is in.
type zonedInstance struct {
	zone     string
	instance *compute.Instance
}

// find returns the instances matching a filter in each of the provider's
// zones, asked in parallel.
func (p *Provider) find(ctx context.Context, filter string) ([]zonedInstance, error) {
	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	found := make([][]zonedInstance, len(p.config.Zones))

	g, ctx := errgroup.WithContext(ctx)
	for i, zone := range p.config.Zones {
		g.Go(func() error {
			return p.compute.Instances.List(p.config.Project, zone).Filter(filter).Pages(ctx,
				func(list *compute.InstanceList) error {
					for _, inst := range list.Items {
						found[i] = append(found[i], zonedInstance{zone: zone, instance: inst})
					}
					return nil
				})
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	var out []zonedInstance
	for _, zone := range found {
		out = append(out, zone...)
	}

	return out, nil
}

// wait waits for a zone operation to finish, and returns its error, if any.
func (p *Provider) wait(ctx context.Context, zone string, op *compute.Operation) error {
	for op.Status != operationDone {
		var err error
		op, err = p.compute.ZoneOperations.Wait(p.config.Project, zone, op.Name).Context(ctx).Do()
		if err != nil {
			return err
		}
	}

	if op.Error != nil && len(op.Error.Errors) > 0 {
		return &operationError{errors: op.Error.Errors}
	}

	return nil
}

// isNotFound reports whether err is Compute Engine saying a resource is not
// there.
func isNotFound(err error) bool {
	var apiErr *googleapi.Error
	return errors.As(err, &apiErr) && apiErr.Code == http.StatusNotFound
}
