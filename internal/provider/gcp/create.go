// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package gcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/api/compute/v1"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

// maxInsertRequests is how many times a request creating an instance is sent
// while Compute Engine leaves unknown whether it created the instance.
const maxInsertRequests = 3

// labelTimeout bounds labelling the disks of an instance created from a
// template, each request in it being bounded by the provider's timeout.
const labelTimeout = 2 * time.Minute

// offerTTL is how long whether a zone offers a machine type is believed.
// Zones gain machine types rarely, and lose them more rarely still.
const offerTTL = time.Hour

// offer is whether a zone offers a machine type, and when it was asked.
type offer struct {
	offered bool
	at      time.Time
}

// attempt is one way of creating a runner's instance: in a zone, of a machine
// type, or of the template's when machineType is empty.
type attempt struct {
	zone, machineType string
}

func (a attempt) String() string {
	if a.machineType == "" {
		return a.zone
	}

	return a.zone + " " + a.machineType
}

// Create creates a runner's instance. Each zone is tried in turn, starting at
// the zone after the scale set's last runner's, and in each zone each of the
// runner's machine types, in order:
//
//   - A zone out of stock of a type, or refusing it, sends the runner on to
//     the next attempt, after deleting whatever it created of the instance.
//   - A zone that does not offer a type is passed over.
//   - A type out of quota is not tried again: a quota is the region's or the
//     project's, so every zone shares it.
//
// When every attempt was out of stock or out of quota, it returns an
// errdefs.ErrNoCapacity error; when an operation failed for another reason,
// an error saying why each attempt was refused. A runner that can never be
// created is not tried again, and neither is one whose request was refused for
// a reason every attempt would share, such as a permission or a rate limit, or
// one Compute Engine did not say whether it created, which might be created
// yet: twice would be one too many. Each request is bounded by the provider's
// timeout, each wait for an operation by waitCallTimeout more, and deleting
// what an attempt left by deleteTimeout; waiting for the instance as a whole
// is bounded by ctx alone, since an instance takes as long as it takes to be
// created.
func (p *Provider) Create(ctx context.Context, spec types.MachineSpec) error {
	ctx, release := p.inFlight.Context(ctx)
	defer release()

	runner, ok := spec.Runner.(RunnerSpec)
	if !ok {
		return errdefs.InvalidArgument("runner %q: not a gcp runner (%T)", spec.Name, spec.Runner)
	}

	// source is the path of the runner's instance template, and
	// instanceTemplate its properties, when it has one.
	var source string
	var instanceTemplate *compute.InstanceProperties
	if runner.InstanceTemplate != "" {
		ref, _ := parseInstanceTemplateRef(runner.InstanceTemplate, p.config.Project)
		source = ref.path()

		var err error
		instanceTemplate, err = p.instanceTemplateProperties(ctx, ref)
		switch {
		case refusalOf(err) == refusalInvalid:
			return errdefs.InvalidArgument("%w", err)
		case err != nil:
			return err
		}
	}

	var reasons []string
	otherRefusal, tried := false, false
	outOfQuota := map[string]bool{}
	for _, try := range p.attempts(runner, spec.Labels[types.LabelScaleSet]) {
		if outOfQuota[try.machineType] {
			continue
		}
		if try.machineType != "" && !p.zoneOffers(ctx, try.zone, try.machineType) {
			reasons = append(reasons, try.String()+": not offered in the zone")
			continue
		}
		tried = true

		instance, err := p.insertInstance(ctx, try, spec, runner, source, instanceTemplate)
		if err == nil {
			if instanceTemplate != nil {
				p.labelInstanceTemplateDisks(ctx, try.zone, instance)
			}
			return nil
		}

		refusal := refusalOf(err)
		switch {
		case ctx.Err() != nil:
			return fmt.Errorf("%s: %w", try, err)
		case refusal == refusalUncertain:
			return fmt.Errorf("%s: Compute Engine did not say whether it created the instance: %w", try, err)
		}

		// A failed operation can leave a stopped instance behind; a refused
		// request creates nothing.
		var operationErr *operationError
		failedOperation := errors.As(err, &operationErr)
		if failedOperation {
			if cleanupErr := p.deleteLeftInstance(ctx, try.zone, spec.Name); cleanupErr != nil {
				return fmt.Errorf("%s: %s; and the instance it may have left could not be deleted: %w",
					try, errorMessage(err), cleanupErr)
			}
		}

		switch {
		case refusal == refusalInvalid:
			return errdefs.InvalidArgument("%s: %w", try, err)
		case refusal == refusalOther && !failedOperation:
			// A request refused for a reason that says nothing of the zone,
			// such as a permission or a rate limit, is refused in every zone.
			return fmt.Errorf("%s: %w", try, err)
		}

		reasons = append(reasons, try.String()+": "+errorMessage(err))
		otherRefusal = otherRefusal || refusal == refusalOther
		if refusal == refusalQuota {
			outOfQuota[try.machineType] = true
		}
	}

	switch {
	case !tried:
		return errdefs.InvalidArgument("no zone offers the machine type: %s", strings.Join(reasons, "; "))
	case otherRefusal:
		return errors.New(strings.Join(reasons, "; "))
	default:
		return errdefs.NoCapacity("%s", strings.Join(reasons, "; "))
	}
}

// attempts returns the ways of creating a scale set's runner's instance, in the
// order they are tried: round the provider's zones from the one after the
// scale set's last runner's, each zone's machine types in the runner's order.
func (p *Provider) attempts(runner RunnerSpec, scaleSet string) []attempt {
	machineTypes := runner.MachineTypes
	if len(machineTypes) == 0 {
		machineTypes = provider.OneOrMore{""}
	}

	zones := p.config.Zones

	p.mu.Lock()
	if p.nextZone == nil {
		p.nextZone = map[string]int{}
	}
	start := p.nextZone[scaleSet]
	p.nextZone[scaleSet] = (start + 1) % len(zones)
	p.mu.Unlock()

	attempts := make([]attempt, 0, len(zones)*len(machineTypes))
	for i := range zones {
		zone := zones[(start+i)%len(zones)]
		for _, machineType := range machineTypes {
			attempts = append(attempts, attempt{zone: zone, machineType: machineType})
		}
	}

	return attempts
}

// insertInstance creates a runner's instance as try says, from the instance
// template at source, whose properties are instanceTemplate, if source is not
// empty, and waits until it is created. While Compute Engine leaves unknown
// whether it took the request, the request is sent again, after a pause, up
// to maxInsertRequests times while ctx lasts: its request ID has Compute
// Engine answer a repeat with the operation it started, if it started one, so
// that the runner gets one instance and knows it.
//
// The request ID is random, one for each attempt: Compute Engine remembers
// one for 60 minutes across the project, and a runner refused here is created
// under the same name by the next attempt or provider, perhaps another gcp
// provider in the project, whose request must not be taken for a repeat of
// this one.
func (p *Provider) insertInstance(ctx context.Context, try attempt, spec types.MachineSpec, runner RunnerSpec,
	source string, instanceTemplate *compute.InstanceProperties,
) (*compute.Instance, error) {
	instance, err := p.config.instanceFor(spec, runner, try.zone, try.machineType, instanceTemplate)
	if err != nil {
		return nil, err
	}

	requestID := uuid.NewString()
	call := p.compute.Instances.Insert(p.config.Project, try.zone, instance).RequestId(requestID)
	if source != "" {
		call = call.SourceInstanceTemplate(source)
	}

	pauses := p.backoff()
	for sent := 1; ; sent++ {
		operation, err := p.insertInstanceOnce(ctx, call)
		if err == nil {
			return instance, p.waitForOperation(ctx, try.zone, operation)
		}
		if refusalOf(err) != refusalUncertain || sent == maxInsertRequests || ctx.Err() != nil {
			return nil, err
		}

		p.logger.Debug("Compute Engine did not say whether it created the runner's instance; asking again",
			slog.String("runner", spec.Name), slog.String("zone", try.zone),
			slog.String("machine_type", try.machineType), slog.String("request_id", requestID),
			slog.Int("request", sent), slog.Any("error", err))

		if !pauses.wait(ctx) {
			return nil, err
		}
	}
}

// insertInstanceOnce sends a request creating an instance, bounded by the
// provider's timeout.
func (p *Provider) insertInstanceOnce(ctx context.Context, call *compute.InstancesInsertCall) (*compute.Operation, error) {
	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	return call.Context(ctx).Do()
}

// deleteLeftInstance deletes the instance a refused attempt left in a zone,
// waiting up to deleteTimeout for it to go.
func (p *Provider) deleteLeftInstance(ctx context.Context, zone, name string) error {
	ctx, cancel := context.WithTimeout(ctx, deleteTimeout)
	defer cancel()

	return p.deleteInstance(ctx, zone, name)
}

// zoneOffers reports whether a zone offers a machine type, asking Compute
// Engine once an hour. A zone it cannot ask is taken to offer it: the insert
// will say if it does not.
func (p *Provider) zoneOffers(ctx context.Context, zone, machineType string) bool {
	key := zone + "/" + machineType

	p.mu.Lock()
	o, ok := p.offers[key]
	p.mu.Unlock()
	if ok && time.Since(o.at) < offerTTL {
		return o.offered
	}

	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	_, err := p.compute.MachineTypes.Get(p.config.Project, zone, machineType).Context(ctx).Do()
	if err != nil && !isNotFound(err) {
		return true
	}

	p.mu.Lock()
	if p.offers == nil {
		p.offers = map[string]offer{}
	}
	p.offers[key] = offer{offered: err == nil, at: time.Now()}
	p.mu.Unlock()

	return err == nil
}

// labelInstanceTemplateDisks gives the disks of an instance created from a
// template the instance's labels, which the template's disks are created
// without, so that their cost is told apart by scale set as the instance's
// is. The runner runs whether or not they are labelled, so a failure is
// logged, not returned, and labelling is bounded by labelTimeout, so as not
// to hold up the runner.
func (p *Provider) labelInstanceTemplateDisks(ctx context.Context, zone string, instance *compute.Instance) {
	ctx, cancel := context.WithTimeout(ctx, labelTimeout)
	defer cancel()

	if err := p.labelDisks(ctx, zone, instance.Name, instance.Labels); err != nil {
		p.logger.Warn("cannot label the disks of an instance created from a template",
			slog.String("runner", instance.Name), slog.String("zone", zone), slog.Any("error", err))
	}
}

// labelDisks adds labels to each disk of an instance, keeping each disk's
// own. Each request is bounded by the provider's timeout, so that an instance
// with many disks has as long for each as one with a single disk.
func (p *Provider) labelDisks(ctx context.Context, zone, name string, labels map[string]string) error {
	callCtx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	instance, err := p.compute.Instances.Get(p.config.Project, zone, name).Context(callCtx).Do()
	cancel()
	if err != nil {
		return err
	}

	for _, attached := range instance.Disks {
		if attached.Source == "" {
			continue // a local SSD, which has no labels
		}
		diskName := path.Base(attached.Source)

		if err := p.labelDisk(ctx, zone, diskName, labels); err != nil {
			return fmt.Errorf("disk %s: %w", diskName, err)
		}
	}

	return nil
}

// labelDisk adds labels to a disk, keeping its own, and waits until they are
// set.
func (p *Provider) labelDisk(ctx context.Context, zone, name string, labels map[string]string) error {
	callCtx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	disk, err := p.compute.Disks.Get(p.config.Project, zone, name).Context(callCtx).Do()
	cancel()
	if err != nil {
		return err
	}

	merged := maps.Clone(disk.Labels)
	if merged == nil {
		merged = map[string]string{}
	}
	maps.Copy(merged, labels)

	callCtx, cancel = context.WithTimeout(ctx, p.config.Timeout)
	operation, err := p.compute.Disks.SetLabels(p.config.Project, zone, name, &compute.ZoneSetLabelsRequest{
		Labels:           merged,
		LabelFingerprint: disk.LabelFingerprint,
	}).Context(callCtx).Do()
	cancel()
	if err != nil {
		return err
	}

	return p.waitForOperation(ctx, zone, operation)
}
