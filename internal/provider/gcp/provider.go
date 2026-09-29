// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package gcp implements the gcp provider type: runners as Compute Engine
// instances in one Google Cloud project and region.
package gcp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/api/compute/v1"

	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

// Provider is a connected Google Cloud project and region. It is safe for
// concurrent use.
type Provider struct {
	config  *Config
	compute *compute.Service
	logger  *slog.Logger

	// inFlight is cancelled by Close.
	inFlight provider.InFlight

	// retryDelay is the first pause before a request is sent again; see
	// backoff.
	retryDelay time.Duration

	mu sync.Mutex

	// nextZone is, by scale set, the index of the zone its next runner
	// starts at, so that each scale set's runners spread across the zones
	// rather than filling the first.
	nextZone map[string]int

	// instanceTemplates are the properties of the instance templates read so
	// far, by path.
	instanceTemplates map[string]*compute.InstanceProperties

	// offers are whether each zone offers each machine type, by
	// zone/machine-type, as last asked.
	offers map[string]offer
}

var _ provider.Provider = (*Provider)(nil)

// operationDone is the status of a finished operation.
const operationDone = "DONE"

// deleteTimeout bounds deleting an instance, which takes longer than a call:
// Compute Engine stops it first.
const deleteTimeout = 5 * time.Minute

// waitCallTimeout is the longest Compute Engine holds a call waiting for an
// operation before answering with the operation as it stands.
const waitCallTimeout = 2 * time.Minute

// List returns the instances in the provider's zones carrying the selector's
// labels.
func (p *Provider) List(ctx context.Context, selector map[string]string) ([]types.Machine, error) {
	ctx, release := p.inFlight.Context(ctx)
	defer release()

	found, err := p.instancesMatching(ctx, labelFilter(selector))
	if err != nil {
		return nil, fmt.Errorf("list instances: %w", err)
	}

	var machines []types.Machine
	for _, f := range found {
		m, ok := machineOf(f.instance)
		if ok && types.HasLabels(m.Labels, selector) {
			machines = append(machines, m)
		}
	}

	return machines, nil
}

// Delete deletes an instance from whichever of the provider's zones it is in,
// waiting up to deleteTimeout for it to go. One not there is not an error.
func (p *Provider) Delete(ctx context.Context, name string) error {
	ctx, release := p.inFlight.Context(ctx)
	defer release()

	ctx, cancel := context.WithTimeout(ctx, deleteTimeout)
	defer cancel()

	found, err := p.instancesMatching(ctx, nameFilter(name))
	if err != nil {
		return fmt.Errorf("find instance %s: %w", name, err)
	}

	for _, f := range found {
		if err := p.deleteInstance(ctx, f.zone, name); err != nil {
			return fmt.Errorf("delete instance %s in %s: %w", name, f.zone, err)
		}
	}

	return nil
}

// deleteInstance deletes an instance from a zone, and waits until it is gone,
// for as long as ctx lasts. One not there, or gone before the deletion
// finished, as a Spot instance Compute Engine takes back can be, is not an
// error.
func (p *Provider) deleteInstance(ctx context.Context, zone, name string) error {
	callCtx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	operation, err := p.compute.Instances.Delete(p.config.Project, zone, name).Context(callCtx).Do()
	cancel()
	if err == nil {
		err = p.waitForOperation(ctx, zone, operation)
	}
	if isNotFound(err) {
		return nil
	}

	return err
}

// Close cancels the calls in flight. The client holds no connection of its
// own to close.
func (p *Provider) Close() error {
	p.inFlight.Cancel()

	return nil
}

// zonedInstance is an instance and the zone it is in.
type zonedInstance struct {
	zone     string
	instance *compute.Instance
}

// instancesMatching returns the instances matching a list filter in each of
// the provider's zones, asked in parallel. It fails if any zone cannot be
// asked: what is there is then unknown, not absent.
func (p *Provider) instancesMatching(ctx context.Context, filter string) ([]zonedInstance, error) {
	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	byZone := make([][]zonedInstance, len(p.config.Zones))

	g, ctx := errgroup.WithContext(ctx)
	for i, zone := range p.config.Zones {
		g.Go(func() error {
			err := p.compute.Instances.List(p.config.Project, zone).Filter(filter).Pages(ctx,
				func(list *compute.InstanceList) error {
					for _, instance := range list.Items {
						byZone[i] = append(byZone[i], zonedInstance{zone: zone, instance: instance})
					}
					return nil
				})
			if err != nil {
				return fmt.Errorf("zone %s: %w", zone, err)
			}

			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	var found []zonedInstance
	for _, instances := range byZone {
		found = append(found, instances...)
	}

	return found, nil
}

// waitForOperation waits for a zone operation to finish, for as long as ctx
// lasts, and returns its error, if any: a *waitError if how it ended could
// not be learnt. Each call waiting is bounded by waitCallTimeout past the
// provider's timeout, since Compute Engine answers one within that. Waiting
// changes nothing, so a call that fails for a reason that may pass, such as a
// server error or no answer in time, is made again after a pause; one Compute
// Engine refuses, as it does an operation it does not know, ends the wait.
func (p *Provider) waitForOperation(ctx context.Context, zone string, operation *compute.Operation) error {
	pauses := p.backoff()
	for operation.Status != operationDone {
		callCtx, cancel := context.WithTimeout(ctx, waitCallTimeout+p.config.Timeout)
		next, err := p.compute.ZoneOperations.Wait(p.config.Project, zone, operation.Name).Context(callCtx).Do()
		cancel()
		if err == nil {
			operation = next
			continue
		}
		if !isTransient(err) || ctx.Err() != nil {
			return &waitError{err: err}
		}

		p.logger.Debug("cannot learn how an operation ended; asking again",
			slog.String("zone", zone), slog.String("operation", operation.Name), slog.Any("error", err))

		if !pauses.wait(ctx) {
			return &waitError{err: err}
		}
	}

	if operation.Error != nil && len(operation.Error.Errors) > 0 {
		return &operationError{errors: operation.Error.Errors}
	}

	return nil
}
