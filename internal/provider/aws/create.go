// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package aws

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/google/uuid"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

// maxRunRequests is how many times a request creating an instance is sent
// while EC2 leaves unknown whether it created the instance.
const maxRunRequests = 3

// attempt is one way of creating a runner's instance: in a subnet, of an
// instance type, or of the launch template's when instanceType is empty.
type attempt struct {
	subnet, instanceType string
}

func (a attempt) String() string {
	if a.instanceType == "" {
		return a.subnet
	}

	return a.subnet + " " + a.instanceType
}

// Create creates a runner's instance. Each of the provider's subnets is tried
// in turn, and in each subnet each of the runner's instance types, in order:
//
//   - A zone out of capacity for a type, or not offering it, sends the
//     runner on to the next attempt.
//   - A type out of quota is not tried again: a quota is the region's, so
//     every subnet shares it.
//
// When every attempt was out of capacity or quota, it returns an
// errdefs.ErrNoCapacity error, and when no subnet offers any of the types, an
// errdefs.ErrInvalidArgument error. A refusal every attempt would share is
// returned at once, as is EC2 leaving unknown whether it created the instance,
// since another attempt might then create a second.
func (p *Provider) Create(ctx context.Context, spec types.MachineSpec) error {
	ctx, release := p.inFlight.Context(ctx)
	defer release()

	runner, ok := spec.Runner.(RunnerSpec)
	if !ok {
		return errdefs.InvalidArgument("runner %q: not an aws runner (%T)", spec.Name, spec.Runner)
	}

	var rootDeviceName string
	if runner.setsRootVolume() {
		var err error
		if rootDeviceName, err = p.rootDeviceName(ctx, runner.Image); err != nil {
			return err
		}
	}

	// exhausted are the attempts out of capacity or quota, which may have
	// room later; an instance type no subnet offers never will.
	var exhausted, unsupported []string
	outOfQuota := map[string]bool{}
	for _, try := range attempts(p.config.Subnets, runner.InstanceTypes) {
		if outOfQuota[try.instanceType] {
			continue
		}

		err := p.runInstance(ctx, try, spec, runner, rootDeviceName)
		if err == nil {
			return nil
		}

		reason := try.String() + ": " + errorMessage(err)
		switch refusalOf(err) {
		case refusalFull:
			exhausted = append(exhausted, reason)
		case refusalQuota:
			exhausted = append(exhausted, reason)
			outOfQuota[try.instanceType] = true
		case refusalUnsupported:
			unsupported = append(unsupported, reason)
		case refusalInvalid:
			return errdefs.InvalidArgument("%s: %w", try, shortError{err})
		case refusalUncertain:
			return fmt.Errorf("%s: EC2 did not say whether it created the instance: %w", try, err)
		default:
			return fmt.Errorf("%s: %w", try, err)
		}
	}

	if len(exhausted) == 0 {
		return errdefs.InvalidArgument("no subnet offers the instance type: %s", strings.Join(unsupported, "; "))
	}

	return errdefs.NoCapacity("%s", strings.Join(append(exhausted, unsupported...), "; "))
}

// attempts returns the ways of creating a runner's instance, in the order they
// are tried: each subnet in turn, and each subnet's instance types in the
// runner's order. No instance types is one attempt per subnet, of the launch
// template's.
func attempts(subnets []string, instanceTypes provider.OneOrMore) []attempt {
	if len(instanceTypes) == 0 {
		instanceTypes = provider.OneOrMore{""}
	}

	out := make([]attempt, 0, len(subnets)*len(instanceTypes))
	for _, subnet := range subnets {
		for _, instanceType := range instanceTypes {
			out = append(out, attempt{subnet: subnet, instanceType: instanceType})
		}
	}

	return out
}

// runInstance creates a runner's instance as try says. While EC2 leaves
// unknown whether it created the instance, the request is sent again, up to
// maxRunRequests times while ctx lasts: its client token has EC2 answer a
// repeat for the instance it created, if it created one, so that the runner
// gets one instance and knows it.
//
// The client token is new for each attempt and kept across that attempt's
// requests alone: EC2 answers a token it has seen with the instance created
// with it, or refuses it with other parameters, so a token shared with
// another attempt, or another provider in the account, would answer the
// runner with an instance that is not its own, or refuse it.
func (p *Provider) runInstance(ctx context.Context, try attempt, spec types.MachineSpec, runner RunnerSpec,
	rootDeviceName string,
) error {
	clientToken := uuid.NewString()
	in := p.config.runInstancesInput(spec, runner, try, rootDeviceName, clientToken)

	for sent := 1; ; sent++ {
		err := p.runInstanceOnce(ctx, in)
		if err == nil || refusalOf(err) != refusalUncertain || sent == maxRunRequests || ctx.Err() != nil {
			return err
		}

		p.logger.Debug("EC2 did not say whether it created the runner's instance; asking again",
			slog.String("runner", spec.Name), slog.String("subnet", try.subnet),
			slog.String("instance_type", try.instanceType), slog.Int("request", sent), slog.Any("error", err))
	}
}

// runInstanceOnce sends a request creating an instance, bounded by the
// provider's timeout.
func (p *Provider) runInstanceOnce(ctx context.Context, in *ec2.RunInstancesInput) error {
	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	_, err := p.ec2.RunInstances(ctx, in)

	return err
}

// rootDeviceName returns the name of an AMI's root device, asking EC2 the
// first time. An AMI EC2 does not have is an errdefs.ErrInvalidArgument
// error.
func (p *Provider) rootDeviceName(ctx context.Context, image string) (string, error) {
	p.mu.Lock()
	device, ok := p.rootDeviceNames[image]
	p.mu.Unlock()
	if ok {
		return device, nil
	}

	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	out, err := p.ec2.DescribeImages(ctx, &ec2.DescribeImagesInput{ImageIds: []string{image}})
	if refusalOf(err) == refusalInvalid {
		return "", errdefs.InvalidArgument("image %s: %w", image, shortError{err})
	}
	if err != nil {
		return "", fmt.Errorf("image %s: %w", image, err)
	}
	if len(out.Images) == 0 || aws.ToString(out.Images[0].RootDeviceName) == "" {
		return "", errdefs.InvalidArgument("image %s: no such AMI in %s", image, p.config.Region)
	}

	device = aws.ToString(out.Images[0].RootDeviceName)

	p.mu.Lock()
	if p.rootDeviceNames == nil {
		p.rootDeviceNames = map[string]string{}
	}
	p.rootDeviceNames[image] = device
	p.mu.Unlock()

	return device, nil
}
