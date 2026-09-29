// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package aws

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"

	"github.com/konradasb/rungar/internal/types"
)

// fakeEC2 is the part of EC2's API the provider calls, in memory.
type fakeEC2 struct {
	mu sync.Mutex

	// instances are in the order they were made.
	instances []*ec2types.Instance

	// images maps each AMI to its root device.
	images map[string]string

	// failures are the error codes each subnet refuses instances with, and
	// describeErr what DescribeInstances fails with.
	failures    map[string]string
	describeErr error

	// pageSize is how many reservations a page of DescribeInstances has.
	pageSize int

	// runs are the RunInstances requests, imageCalls how many times
	// DescribeImages was called, and terminates the instances terminated.
	runs       []*ec2.RunInstancesInput
	imageCalls int
	terminates [][]string
}

var _ ec2API = (*fakeEC2)(nil)

func newFakeEC2() *fakeEC2 {
	return &fakeEC2{
		images:   map[string]string{"ami-0123456789abcdef0": "/dev/xvda"},
		failures: map[string]string{},
		pageSize: 1,
	}
}

func (f *fakeEC2) RunInstances(_ context.Context, in *ec2.RunInstancesInput, _ ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.runs = append(f.runs, in)

	subnet := aws.ToString(in.NetworkInterfaces[0].SubnetId)
	if code := f.failures[subnet]; code != "" {
		return nil, &smithy.GenericAPIError{Code: code, Message: "the subnet said no"}
	}

	inst := &ec2types.Instance{
		InstanceId:   aws.String(fmt.Sprintf("i-%017x", len(f.instances)+1)),
		InstanceType: in.InstanceType,
		SubnetId:     aws.String(subnet),
		State:        &ec2types.InstanceState{Name: ec2types.InstanceStateNamePending},
		LaunchTime:   aws.Time(time.Date(2026, 9, 29, 17, 0, 0, 0, time.UTC)),
	}
	for _, ts := range in.TagSpecifications {
		if ts.ResourceType == ec2types.ResourceTypeInstance {
			inst.Tags = ts.Tags
		}
	}
	f.instances = append(f.instances, inst)

	return &ec2.RunInstancesOutput{Instances: []ec2types.Instance{*inst}}, nil
}

func (f *fakeEC2) DescribeInstances(_ context.Context, in *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.describeErr != nil {
		return nil, f.describeErr
	}

	var matched []ec2types.Instance
	for _, inst := range f.instances {
		if matchesFilters(inst, in.Filters) {
			matched = append(matched, *inst)
		}
	}

	start, _ := strconv.Atoi(aws.ToString(in.NextToken))
	end := min(start+f.pageSize, len(matched))

	out := &ec2.DescribeInstancesOutput{}
	for _, inst := range matched[start:end] {
		out.Reservations = append(out.Reservations, ec2types.Reservation{Instances: []ec2types.Instance{inst}})
	}
	if end < len(matched) {
		out.NextToken = aws.String(strconv.Itoa(end))
	}

	return out, nil
}

func (f *fakeEC2) TerminateInstances(_ context.Context, in *ec2.TerminateInstancesInput, _ ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.terminates = append(f.terminates, in.InstanceIds)

	for _, id := range in.InstanceIds {
		inst := f.byID(id)
		if inst == nil {
			return nil, &smithy.GenericAPIError{Code: "InvalidInstanceID.NotFound", Message: "no " + id}
		}
		inst.State = &ec2types.InstanceState{Name: ec2types.InstanceStateNameShuttingDown}
	}

	return &ec2.TerminateInstancesOutput{}, nil
}

func (f *fakeEC2) DescribeImages(_ context.Context, in *ec2.DescribeImagesInput, _ ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.imageCalls++

	out := &ec2.DescribeImagesOutput{}
	for _, id := range in.ImageIds {
		device, ok := f.images[id]
		if !ok {
			return nil, &smithy.GenericAPIError{Code: "InvalidAMIID.NotFound", Message: "no " + id}
		}
		out.Images = append(out.Images, ec2types.Image{ImageId: aws.String(id), RootDeviceName: aws.String(device)})
	}

	return out, nil
}

// byID returns the instance of this ID, or nil.
func (f *fakeEC2) byID(id string) *ec2types.Instance {
	for _, inst := range f.instances {
		if aws.ToString(inst.InstanceId) == id {
			return inst
		}
	}

	return nil
}

// named returns the instance named name, or nil.
func (f *fakeEC2) named(name string) *ec2types.Instance {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, inst := range f.instances {
		if tagValue(inst.Tags, nameTag) == name {
			return inst
		}
	}

	return nil
}

// lastRun returns the last RunInstances request.
func (f *fakeEC2) lastRun() *ec2.RunInstancesInput {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.runs[len(f.runs)-1]
}

// runSubnets returns the subnet each RunInstances request was for.
func (f *fakeEC2) runSubnets() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	var subnets []string
	for _, in := range f.runs {
		subnets = append(subnets, aws.ToString(in.NetworkInterfaces[0].SubnetId))
	}

	return subnets
}

// matchesFilters reports whether an instance matches every filter the
// provider uses: tag:, subnet-id and instance-state-name.
func matchesFilters(inst *ec2types.Instance, filters []ec2types.Filter) bool {
	for _, filter := range filters {
		var got string

		switch name := aws.ToString(filter.Name); {
		case strings.HasPrefix(name, "tag:"):
			got = tagValue(inst.Tags, strings.TrimPrefix(name, "tag:"))
		case name == "subnet-id":
			got = aws.ToString(inst.SubnetId)
		case name == "instance-state-name":
			got = string(inst.State.Name)
		default:
			panic("fake: unknown filter " + name)
		}

		if !slices.Contains(filter.Values, got) {
			return false
		}
	}

	return true
}

// testConfig returns a configuration of three subnets in eu-west-1.
func testConfig() *Config {
	return &Config{
		Region:  "eu-west-1",
		Subnets: []string{"subnet-0000000000000000a", "subnet-0000000000000000b", "subnet-0000000000000000c"},
		Timeout: defaultTimeout,
	}
}

// newTestProvider returns a provider of config over the fake.
func newTestProvider(config *Config, f *fakeEC2) *Provider {
	return config.open(nil, f)
}

// testRunner returns a runner block with its defaults.
func testRunner() RunnerSpec {
	return RunnerSpec{InstanceType: "m7i.xlarge", Image: "ami-0123456789abcdef0"}.withDefaults()
}

// testMachine returns the machine spec of a runner of scale set, as Rungar
// makes it.
func testMachine(name, scaleSet string) types.MachineSpec {
	return types.MachineSpec{
		Name:      name,
		Labels:    types.RunnerLabels("gh-4e3651f01be5", scaleSet, name, "a1b2c3"),
		JITConfig: "jit-" + name,
		Runner:    testRunner(),
	}
}

// otherRunner is a runner block of another provider type.
type otherRunner struct{}

func (otherRunner) Describe() string { return "other" }
