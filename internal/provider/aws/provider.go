// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package aws implements the aws provider type: runners as EC2 instances in
// one AWS account and region.
package aws

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

// ec2API is the part of EC2's API the provider calls.
type ec2API interface {
	RunInstances(ctx context.Context, in *ec2.RunInstancesInput, opts ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error)
	DescribeInstances(ctx context.Context, in *ec2.DescribeInstancesInput, opts ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	TerminateInstances(ctx context.Context, in *ec2.TerminateInstancesInput, opts ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
	DescribeImages(ctx context.Context, in *ec2.DescribeImagesInput, opts ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error)
}

// Provider is a connected AWS account and region.
type Provider struct {
	config *Config
	ec2    ec2API
	logger *slog.Logger

	// rootDevices maps each AMI asked about to its root device's name,
	// which the root volume's size is set on.
	mu          sync.Mutex
	rootDevices map[string]string
}

var _ provider.Provider = (*Provider)(nil)

// liveStates are the states of an instance not yet shutting down: one
// shutting down or terminated is gone, for Rungar.
var liveStates = []string{
	string(ec2types.InstanceStateNamePending),
	string(ec2types.InstanceStateNameRunning),
	string(ec2types.InstanceStateNameStopping),
	string(ec2types.InstanceStateNameStopped),
}

// List returns the live instances in the provider's subnets carrying the
// selector's labels.
func (p *Provider) List(ctx context.Context, selector map[string]string) ([]types.Machine, error) {
	instances, err := p.find(ctx, tagFilters(selector))
	if err != nil {
		return nil, err
	}

	var out []types.Machine
	for _, inst := range instances {
		m := machineOf(inst)
		if types.Matches(m.Labels, selector) {
			out = append(out, m)
		}
	}

	return out, nil
}

// Create makes a runner's instance in the first of the provider's subnets
// that has room for it. A zone out of capacity, or not offering the
// instance type, sends it to the next subnet; when every subnet is full, or
// the account is out of quota, it returns an errdefs.NoCapacity error.
func (p *Provider) Create(ctx context.Context, spec types.MachineSpec) error {
	runner, ok := spec.Runner.(RunnerSpec)
	if !ok {
		return errdefs.InvalidArgument("runner %q: not an aws runner (%T)", spec.Name, spec.Runner)
	}

	rootDevice, err := p.rootDevice(ctx, runner.Image)
	if err != nil {
		return err
	}

	var full, unsupported []string
	for _, subnet := range p.config.Subnets {
		err := p.createIn(ctx, subnet, spec, runner, rootDevice)
		if err == nil {
			return nil
		}

		switch classify(err) {
		case refusalFull:
			full = append(full, fmt.Sprintf("%s: %s", subnet, errorMessage(err)))
		case refusalUnsupported:
			unsupported = append(unsupported, fmt.Sprintf("%s: %s", subnet, errorMessage(err)))
		case refusalQuota:
			return errdefs.NoCapacity("%s", errorMessage(err))
		case refusalInvalid:
			return errdefs.InvalidArgument("%s", errorMessage(err))
		default:
			return fmt.Errorf("%s: %w", subnet, err)
		}
	}

	// A subnet out of capacity may have room later; an instance type no
	// subnet offers never will.
	if len(full) == 0 {
		return errdefs.InvalidArgument("%s", strings.Join(unsupported, "; "))
	}

	return errdefs.NoCapacity("%s", strings.Join(append(full, unsupported...), "; "))
}

// createIn makes a runner's instance in a subnet.
func (p *Provider) createIn(ctx context.Context, subnet string, spec types.MachineSpec, runner RunnerSpec, rootDevice string) error {
	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	_, err := p.ec2.RunInstances(ctx, p.config.runInstancesInput(spec, runner, subnet, rootDevice))

	return err
}

// Delete terminates the instance of this name, which EC2 stops first. It does
// not wait for the instance to go: one shutting down is gone, for List. One
// not there is not an error.
func (p *Provider) Delete(ctx context.Context, name string) error {
	instances, err := p.find(ctx, append(tagFilters(map[string]string{types.LabelManaged: "true"}),
		ec2types.Filter{Name: aws.String("tag:" + nameTag), Values: []string{name}}))
	if err != nil {
		return err
	}
	if len(instances) == 0 {
		return nil
	}

	ids := make([]string, 0, len(instances))
	for _, inst := range instances {
		ids = append(ids, aws.ToString(inst.InstanceId))
	}

	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	_, err = p.ec2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: ids})
	if isNotFound(err) {
		return nil
	}

	return err
}

// Close does nothing: the client holds no connection of its own.
func (p *Provider) Close() error {
	return nil
}

// find returns the live instances in the provider's subnets matching filters.
func (p *Provider) find(ctx context.Context, filters []ec2types.Filter) ([]ec2types.Instance, error) {
	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	in := &ec2.DescribeInstancesInput{
		Filters: append(filters,
			ec2types.Filter{Name: aws.String("subnet-id"), Values: p.config.Subnets},
			ec2types.Filter{Name: aws.String("instance-state-name"), Values: liveStates}),
	}

	var out []ec2types.Instance

	pages := ec2.NewDescribeInstancesPaginator(p.ec2, in)
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, r := range page.Reservations {
			out = append(out, r.Instances...)
		}
	}

	return out, nil
}

// rootDevice returns the name of an AMI's root device, asking EC2 the first
// time. An AMI EC2 does not have is an invalid argument.
func (p *Provider) rootDevice(ctx context.Context, image string) (string, error) {
	p.mu.Lock()
	device, ok := p.rootDevices[image]
	p.mu.Unlock()
	if ok {
		return device, nil
	}

	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	out, err := p.ec2.DescribeImages(ctx, &ec2.DescribeImagesInput{ImageIds: []string{image}})
	if classify(err) == refusalInvalid {
		return "", errdefs.InvalidArgument("image %s: %s", image, errorMessage(err))
	}
	if err != nil {
		return "", fmt.Errorf("image %s: %w", image, err)
	}
	if len(out.Images) == 0 || aws.ToString(out.Images[0].RootDeviceName) == "" {
		return "", errdefs.InvalidArgument("image %s: no such AMI in %s", image, p.config.Region)
	}

	device = aws.ToString(out.Images[0].RootDeviceName)

	p.mu.Lock()
	p.rootDevices[image] = device
	p.mu.Unlock()

	return device, nil
}

// tagFilters returns the filters matching the instances carrying every label
// in selector as a tag.
func tagFilters(selector map[string]string) []ec2types.Filter {
	filters := make([]ec2types.Filter, 0, len(selector))
	for _, key := range slices.Sorted(maps.Keys(selector)) {
		filters = append(filters, ec2types.Filter{Name: aws.String("tag:" + key), Values: []string{selector[key]}})
	}

	return filters
}
