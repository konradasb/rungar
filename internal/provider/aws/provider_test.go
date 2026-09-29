// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package aws

import (
	"context"
	"encoding/base64"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

func TestCreateMakesTheInstance(t *testing.T) {
	f := newFakeEC2()
	config := testConfig()
	config.SecurityGroups = []string{"sg-0a1b2c3d4e5f60718"}
	config.PublicIP = aws.Bool(false)
	config.InstanceProfile = "rungar-runner"
	p := newTestProvider(config, f)

	spec := testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8")
	runner := testRunner()
	runner.Tags = map[string]string{"team": "ci"}
	spec.Runner = runner

	if err := p.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	in := f.lastRun()
	nic := in.NetworkInterfaces[0]
	disk := in.BlockDeviceMappings[0]
	userData, err := base64.StdEncoding.DecodeString(aws.ToString(in.UserData))
	if err != nil {
		t.Fatalf("user data is not base64: %v", err)
	}

	var instanceTags []ec2types.Tag
	for _, ts := range in.TagSpecifications {
		if ts.ResourceType == ec2types.ResourceTypeInstance {
			instanceTags = ts.Tags
		}
	}

	checks := []struct {
		what      string
		got, want any
	}{
		{"instance type", string(in.InstanceType), "m7i.xlarge"},
		{"image", aws.ToString(in.ImageId), "ami-0123456789abcdef0"},
		{"subnet", aws.ToString(nic.SubnetId), config.Subnets[0]},
		{"security groups", strings.Join(nic.Groups, ","), "sg-0a1b2c3d4e5f60718"},
		{"public address", aws.ToBool(nic.AssociatePublicIpAddress), false},
		{"instance profile", aws.ToString(in.IamInstanceProfile.Name), "rungar-runner"},
		{"root device", aws.ToString(disk.DeviceName), "/dev/xvda"},
		{"disk size", aws.ToInt32(disk.Ebs.VolumeSize), int32(50)},
		{"disk type", string(disk.Ebs.VolumeType), "gp3"},
		{"disk deleted", aws.ToBool(disk.Ebs.DeleteOnTermination), true},
		{"on shutdown", in.InstanceInitiatedShutdownBehavior, ec2types.ShutdownBehaviorTerminate},
		{"IMDSv2", in.MetadataOptions.HttpTokens, ec2types.HttpTokensStateRequired},
		{"spot", in.InstanceMarketOptions == nil, true},
		{"name tag", tagValue(instanceTags, nameTag), spec.Name},
		{"managed tag", tagValue(instanceTags, types.LabelManaged), "true"},
		{"scale set tag", tagValue(instanceTags, types.LabelScaleSet), "rungar-c4-m8"},
		{"extra tag", tagValue(instanceTags, "team"), "ci"},
		{"volume tagged", len(in.TagSpecifications), 2},
		{"user data", string(userData), "#!/bin/bash\nexport ACTIONS_RUNNER_INPUT_JITCONFIG='jit-rungar-c4-m8-1a2b3c4d'\n" +
			strings.SplitN(defaultUserData, "\n", 2)[1]},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.what, c.got, c.want)
		}
	}
}

func TestCreateOnSpot(t *testing.T) {
	f := newFakeEC2()
	p := newTestProvider(testConfig(), f)

	spec := testMachine("rungar-spot-1", "rungar-spot")
	runner := testRunner()
	runner.Spot = true
	spec.Runner = runner

	if err := p.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	m := f.lastRun().InstanceMarketOptions
	if m == nil || m.MarketType != ec2types.MarketTypeSpot ||
		m.SpotOptions.InstanceInterruptionBehavior != ec2types.InstanceInterruptionBehaviorTerminate {
		t.Errorf("market options = %+v, want a Spot Instance terminated when taken back", m)
	}
}

func TestCreateByInstanceProfileARN(t *testing.T) {
	f := newFakeEC2()
	config := testConfig()
	config.InstanceProfile = "arn:aws:iam::123456789012:instance-profile/rungar-runner"
	p := newTestProvider(config, f)

	if err := p.Create(context.Background(), testMachine("rungar-x-1", "rungar-x")); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	if got := f.lastRun().IamInstanceProfile; aws.ToString(got.Arn) != config.InstanceProfile || got.Name != nil {
		t.Errorf("instance profile = %+v, want the ARN", got)
	}
}

func TestCreateAsksForTheRootDeviceOnce(t *testing.T) {
	f := newFakeEC2()
	f.images["ami-0123456789abcdef0"] = "/dev/sda1"
	p := newTestProvider(testConfig(), f)
	ctx := context.Background()

	for _, name := range []string{"rungar-x-1", "rungar-x-2"} {
		if err := p.Create(ctx, testMachine(name, "rungar-x")); err != nil {
			t.Fatalf("Create(%s) = %v", name, err)
		}
	}

	if got := aws.ToString(f.lastRun().BlockDeviceMappings[0].DeviceName); got != "/dev/sda1" {
		t.Errorf("root device = %q, want the AMI's", got)
	}
	if f.imageCalls != 1 {
		t.Errorf("asked for the AMI %d times, want once", f.imageCalls)
	}
}

func TestCreateRefusesAnImageEC2DoesNotHave(t *testing.T) {
	f := newFakeEC2()
	p := newTestProvider(testConfig(), f)

	spec := testMachine("rungar-x-1", "rungar-x")
	runner := testRunner()
	runner.Image = "ami-0fffffffffffffff0"
	spec.Runner = runner

	if err := p.Create(context.Background(), spec); !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("Create() = %v, want invalid argument", err)
	}
	if len(f.runs) != 0 {
		t.Errorf("made %d instances, want none", len(f.runs))
	}
}

func TestCreateTriesTheNextSubnetWhenOneIsFull(t *testing.T) {
	f := newFakeEC2()
	config := testConfig()
	f.failures[config.Subnets[0]] = "InsufficientInstanceCapacity"
	f.failures[config.Subnets[1]] = "Unsupported"
	p := newTestProvider(config, f)

	spec := testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8")
	if err := p.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create() = %v, want the instance made in the third subnet", err)
	}

	if got := aws.ToString(f.named(spec.Name).SubnetId); got != config.Subnets[2] {
		t.Errorf("instance in %s, want %s", got, config.Subnets[2])
	}
	if got := f.runSubnets(); !slices.Equal(got, config.Subnets) {
		t.Errorf("tried %v, want the subnets in order", got)
	}

	// Each subnet's request has its own token, so EC2 does not take the
	// next for a repeat of the last.
	tokens := map[string]bool{}
	for _, in := range f.runs {
		tokens[aws.ToString(in.ClientToken)] = true
	}
	if len(tokens) != 3 {
		t.Errorf("%d client tokens for 3 requests, want one each", len(tokens))
	}
}

func TestCreateIsNoCapacityWhenEverySubnetIsFull(t *testing.T) {
	f := newFakeEC2()
	config := testConfig()
	f.failures[config.Subnets[0]] = "InsufficientInstanceCapacity"
	f.failures[config.Subnets[1]] = "InsufficientFreeAddressesInSubnet"
	f.failures[config.Subnets[2]] = "Unsupported"
	p := newTestProvider(config, f)

	err := p.Create(context.Background(), testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8"))
	if !errors.Is(err, errdefs.ErrNoCapacity) {
		t.Fatalf("Create() = %v, want no capacity", err)
	}
	for _, subnet := range config.Subnets {
		if !strings.Contains(err.Error(), subnet) {
			t.Errorf("error %q does not say why %s refused", err, subnet)
		}
	}
}

func TestCreateStopsAtTheAccountsQuota(t *testing.T) {
	f := newFakeEC2()
	config := testConfig()
	for _, subnet := range config.Subnets {
		f.failures[subnet] = "VcpuLimitExceeded"
	}
	p := newTestProvider(config, f)

	err := p.Create(context.Background(), testMachine("rungar-x-1", "rungar-x"))
	if !errors.Is(err, errdefs.ErrNoCapacity) {
		t.Fatalf("Create() = %v, want no capacity", err)
	}

	// The quota is the region's: the next subnet shares it.
	if n := len(f.runs); n != 1 {
		t.Errorf("tried %d subnets, want 1", n)
	}
}

func TestCreateSaysWhyItRefused(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		invalid  bool
		subnets  int
		capacity bool
	}{
		{"unknown AMI", "InvalidAMIID.NotFound", true, 1, false},
		{"bad parameter", "InvalidParameterValue", true, 1, false},
		{"unknown security group", "InvalidGroup.NotFound", true, 1, false},
		{"type offered nowhere", "Unsupported", true, 3, false},
		{"unauthorized", "UnauthorizedOperation", false, 1, false},
		{"internal error", "InternalError", false, 1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeEC2()
			for _, subnet := range testConfig().Subnets {
				f.failures[subnet] = tt.code
			}
			p := newTestProvider(testConfig(), f)

			err := p.Create(context.Background(), testMachine("rungar-x-1", "rungar-x"))
			if err == nil {
				t.Fatal("Create() = nil, want an error")
			}

			if errors.Is(err, errdefs.ErrNoCapacity) {
				t.Errorf("Create() = %v, want not no capacity", err)
			}
			if got := errors.Is(err, errdefs.ErrInvalidArgument); got != tt.invalid {
				t.Errorf("Create() = %v; invalid argument %v, want %v", err, got, tt.invalid)
			}
			if n := len(f.runs); n != tt.subnets {
				t.Errorf("tried %d subnets, want %d", n, tt.subnets)
			}
		})
	}
}

func TestCreateRefusesAnotherTypesRunner(t *testing.T) {
	p := newTestProvider(testConfig(), newFakeEC2())

	spec := testMachine("rungar-x-1", "rungar-x")
	spec.Runner = otherRunner{}

	if err := p.Create(context.Background(), spec); !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("Create() = %v, want invalid argument", err)
	}
}

func TestListReturnsTheScaleSetsMachines(t *testing.T) {
	f := newFakeEC2()
	config := testConfig()
	f.failures[config.Subnets[0]] = "InsufficientInstanceCapacity"
	p := newTestProvider(config, f)
	ctx := context.Background()

	mine := testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8")
	gone := testMachine("rungar-c4-m8-5e6f7a8b", "rungar-c4-m8")
	other := testMachine("rungar-c2-m4-5e6f7a8b", "rungar-c2-m4")
	for _, spec := range []types.MachineSpec{mine, gone, other} {
		if err := p.Create(ctx, spec); err != nil {
			t.Fatalf("Create(%s) = %v", spec.Name, err)
		}
	}
	if err := p.Delete(ctx, gone.Name); err != nil {
		t.Fatalf("Delete() = %v", err)
	}

	// An instance of the scale set's, in a subnet not the provider's.
	f.mu.Lock()
	stray := *f.instances[0]
	stray.InstanceId = aws.String("i-stray")
	stray.SubnetId = aws.String("subnet-0ffffffffffffffff")
	f.instances = append(f.instances, &stray)
	f.mu.Unlock()

	selector := types.ScaleSetSelector("gh-4e3651f01be5", "rungar-c4-m8")
	got, err := p.List(ctx, selector)
	if err != nil {
		t.Fatalf("List() = %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("List() = %+v, want the scale set's one live machine", got)
	}

	m := got[0]
	want := types.Machine{
		Name:      mine.Name,
		Labels:    mine.Labels,
		State:     types.MachineStarting,
		Size:      "m7i.xlarge",
		CreatedAt: time.Date(2026, 9, 29, 17, 0, 0, 0, time.UTC),
	}
	if m.Name != want.Name || !maps.Equal(m.Labels, want.Labels) || m.State != want.State ||
		m.Size != want.Size || !m.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("List()[0] = %+v, want %+v", m, want)
	}
}

func TestListFailsWhenEC2Does(t *testing.T) {
	f := newFakeEC2()
	f.describeErr = &smithy.GenericAPIError{Code: "RequestLimitExceeded", Message: "slow down"}
	p := newTestProvider(testConfig(), f)

	if _, err := p.List(context.Background(), map[string]string{types.LabelManaged: "true"}); err == nil {
		t.Error("List() = nil error, want one: what the region has is unknown")
	}
}

func TestMachineState(t *testing.T) {
	tests := map[ec2types.InstanceStateName]types.MachineState{
		ec2types.InstanceStateNamePending:  types.MachineStarting,
		ec2types.InstanceStateNameRunning:  types.MachineRunning,
		ec2types.InstanceStateNameStopping: types.MachineStopped,
		ec2types.InstanceStateNameStopped:  types.MachineStopped,
	}

	for state, want := range tests {
		if got := machineState(state); got != want {
			t.Errorf("machineState(%s) = %s, want %s", state, got, want)
		}
	}
}

func TestDelete(t *testing.T) {
	f := newFakeEC2()
	p := newTestProvider(testConfig(), f)
	ctx := context.Background()

	spec := testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8")
	if err := p.Create(ctx, spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	if err := p.Delete(ctx, spec.Name); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if got := f.named(spec.Name).State.Name; got != ec2types.InstanceStateNameShuttingDown {
		t.Errorf("instance is %s, want shutting-down", got)
	}

	if err := p.Delete(ctx, spec.Name); err != nil {
		t.Errorf("Delete() of an instance already gone = %v, want nil", err)
	}
	if n := len(f.terminates); n != 1 {
		t.Errorf("terminated %d times, want once", n)
	}
	if got := f.terminates[0]; !slices.Equal(got, []string{aws.ToString(f.named(spec.Name).InstanceId)}) {
		t.Errorf("terminated %v, want the instance", got)
	}
}

func TestDeleteLeavesOthersInstances(t *testing.T) {
	f := newFakeEC2()
	p := newTestProvider(testConfig(), f)

	// An instance of the same name that is not Rungar's.
	f.instances = append(f.instances, &ec2types.Instance{
		InstanceId: aws.String("i-stranger"),
		SubnetId:   aws.String(testConfig().Subnets[0]),
		State:      &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning},
		Tags:       []ec2types.Tag{{Key: aws.String(nameTag), Value: aws.String("rungar-x-1")}},
	})

	if err := p.Delete(context.Background(), "rungar-x-1"); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if len(f.terminates) != 0 {
		t.Errorf("terminated %v, want nothing", f.terminates)
	}
}
