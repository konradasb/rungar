// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package aws implements the aws provider type: runners as EC2 instances in
// one AWS account and region.
package aws

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

// ec2API is the part of EC2's API the provider calls.
type ec2API interface {
	RunInstances(ctx context.Context, in *ec2.RunInstancesInput,
		opts ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error)
	DescribeInstances(ctx context.Context, in *ec2.DescribeInstancesInput,
		opts ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	TerminateInstances(ctx context.Context, in *ec2.TerminateInstancesInput,
		opts ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
	DescribeImages(ctx context.Context, in *ec2.DescribeImagesInput,
		opts ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error)
}

// Provider is a connected AWS account and region. It is safe for concurrent
// use.
type Provider struct {
	config *Config
	ec2    ec2API
	logger *slog.Logger

	// inFlight is cancelled by Close.
	inFlight provider.InFlight

	mu sync.Mutex

	// rootDeviceNames maps each AMI asked about to its root device's name,
	// which the root volume's size is set on. It is nil until the first AMI
	// is asked about.
	rootDeviceNames map[string]string
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
	ctx, release := p.inFlight.Context(ctx)
	defer release()

	instances, err := p.instancesMatching(ctx, tagFilters(selector))
	if err != nil {
		return nil, fmt.Errorf("list instances: %w", err)
	}

	var out []types.Machine
	for _, instance := range instances {
		m := machineOf(instance)
		if types.HasLabels(m.Labels, selector) {
			out = append(out, m)
		}
	}

	return out, nil
}

// Delete terminates the instance of this name, which EC2 stops first. It does
// not wait for the instance to go: one shutting down is gone, for List. One
// not there is not an error.
func (p *Provider) Delete(ctx context.Context, name string) error {
	ctx, release := p.inFlight.Context(ctx)
	defer release()

	instances, err := p.instancesMatching(ctx, tagFilters(map[string]string{
		types.LabelManaged: "true",
		nameTag:            name,
	}))
	if err != nil {
		return fmt.Errorf("find instance %s: %w", name, err)
	}
	if len(instances) == 0 {
		return nil
	}

	ids := make([]string, 0, len(instances))
	for _, instance := range instances {
		ids = append(ids, aws.ToString(instance.InstanceId))
	}

	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	_, err = p.ec2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: ids})
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("terminate instance %s: %w", name, err)
	}

	return nil
}

// Close cancels the calls in flight. The client holds no connection of its
// own to close.
func (p *Provider) Close() error {
	p.inFlight.Cancel()

	return nil
}

// instancesMatching returns the live instances in the provider's subnets
// matching filters.
func (p *Provider) instancesMatching(ctx context.Context, filters []ec2types.Filter) ([]ec2types.Instance, error) {
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
