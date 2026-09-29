// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package aws

import (
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

// TestCreateSendsTheRunnersInstance checks the request Create sends carries
// the provider's and the runner's settings.
func TestCreateSendsTheRunnersInstance(t *testing.T) {
	f := newFakeEC2(t)
	config := testConfig()
	config.SecurityGroups = []string{"sg-0a1b2c3d4e5f60718"}
	config.PublicIP = aws.Bool(false)
	config.InstanceProfile = "rungar-runner"
	p := newTestProvider(config, f)

	spec := testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8")
	runner := testRunner()
	runner.Tags = map[string]string{"team": "ci"}
	spec.Runner = runner

	if err := p.Create(t.Context(), spec); err != nil {
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

// TestCreateAsksForASpotInstance checks a Spot runner is created as a Spot
// Instance that is terminated, not stopped, when EC2 takes it back.
func TestCreateAsksForASpotInstance(t *testing.T) {
	f := newFakeEC2(t)
	p := newTestProvider(testConfig(), f)

	spec := testMachine("rungar-spot-1", "rungar-spot")
	runner := testRunner()
	runner.Spot = true
	spec.Runner = runner

	if err := p.Create(t.Context(), spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	m := f.lastRun().InstanceMarketOptions
	if m == nil || m.MarketType != ec2types.MarketTypeSpot ||
		m.SpotOptions.InstanceInterruptionBehavior != ec2types.InstanceInterruptionBehaviorTerminate {
		t.Errorf("market options = %+v, want a Spot Instance terminated when taken back", m)
	}
}

func TestCreateByInstanceProfileARN(t *testing.T) {
	f := newFakeEC2(t)
	config := testConfig()
	config.InstanceProfile = "arn:aws:iam::123456789012:instance-profile/rungar-runner"
	p := newTestProvider(config, f)

	if err := p.Create(t.Context(), testMachine("rungar-x-1", "rungar-x")); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	if got := f.lastRun().IamInstanceProfile; aws.ToString(got.Arn) != config.InstanceProfile || got.Name != nil {
		t.Errorf("instance profile = %+v, want the ARN", got)
	}
}

func TestCreateAsksForTheRootDeviceOnce(t *testing.T) {
	f := newFakeEC2(t)
	f.images["ami-0123456789abcdef0"] = "/dev/sda1"
	p := newTestProvider(testConfig(), f)
	ctx := t.Context()

	for _, name := range []string{"rungar-x-1", "rungar-x-2"} {
		if err := p.Create(ctx, testMachine(name, "rungar-x")); err != nil {
			t.Fatalf("Create(%s) = %v", name, err)
		}
	}

	if got := aws.ToString(f.lastRun().BlockDeviceMappings[0].DeviceName); got != "/dev/sda1" {
		t.Errorf("root device = %q, want the AMI's", got)
	}
	if n := f.imageCallCount(); n != 1 {
		t.Errorf("asked for the AMI %d times, want once", n)
	}
}

func TestCreateRefusesAnImageEC2DoesNotHave(t *testing.T) {
	f := newFakeEC2(t)
	p := newTestProvider(testConfig(), f)

	spec := testMachine("rungar-x-1", "rungar-x")
	runner := testRunner()
	runner.Image = "ami-0fffffffffffffff0"
	spec.Runner = runner

	err := p.Create(t.Context(), spec)
	if !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("Create() = %v, want invalid argument", err)
	}
	checkWrapsEC2Error(t, err, "InvalidAMIID.NotFound")
	if n := len(f.runRequests()); n != 0 {
		t.Errorf("created %d instances, want none", n)
	}
}

func TestCreateTriesTheNextSubnetWhenOneIsFull(t *testing.T) {
	f := newFakeEC2(t)
	config := testConfig()
	f.failures[config.Subnets[0]] = "InsufficientInstanceCapacity"
	f.failures[config.Subnets[1]] = "Unsupported"
	p := newTestProvider(config, f)

	spec := testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8")
	if err := p.Create(t.Context(), spec); err != nil {
		t.Fatalf("Create() = %v, want the instance created in the third subnet", err)
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
	for _, in := range f.runRequests() {
		tokens[aws.ToString(in.ClientToken)] = true
	}
	if len(tokens) != 3 {
		t.Errorf("%d client tokens for 3 requests, want one each", len(tokens))
	}
}

func TestCreateIsNoCapacityWhenEverySubnetIsFull(t *testing.T) {
	f := newFakeEC2(t)
	config := testConfig()
	f.failures[config.Subnets[0]] = "InsufficientInstanceCapacity"
	f.failures[config.Subnets[1]] = "InsufficientFreeAddressesInSubnet"
	f.failures[config.Subnets[2]] = "Unsupported"
	p := newTestProvider(config, f)

	err := p.Create(t.Context(), testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8"))
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
	f := newFakeEC2(t)
	config := testConfig()
	for _, subnet := range config.Subnets {
		f.failures[subnet] = "VcpuLimitExceeded"
	}
	p := newTestProvider(config, f)

	err := p.Create(t.Context(), testMachine("rungar-x-1", "rungar-x"))
	if !errors.Is(err, errdefs.ErrNoCapacity) {
		t.Fatalf("Create() = %v, want no capacity", err)
	}

	// The quota is the region's: the next subnet shares it.
	if n := len(f.runRequests()); n != 1 {
		t.Errorf("tried %d subnets, want 1", n)
	}
}

// TestCreateSaysWhyItRefused checks how each refusal is returned, how many
// subnets it is tried in, and that only a request whose outcome EC2 left
// unknown is sent again.
func TestCreateSaysWhyItRefused(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		invalid  bool
		subnets  int
		requests int
	}{
		{"unknown AMI", "InvalidAMIID.NotFound", true, 1, 1},
		{"bad parameter", "InvalidParameterValue", true, 1, 1},
		{"unknown security group", "InvalidGroup.NotFound", true, 1, 1},
		{"type offered nowhere", "Unsupported", true, 3, 3},
		{"unauthorized", "UnauthorizedOperation", false, 1, 1},
		{"internal error", "InternalError", false, 1, maxRunRequests},
		{"unavailable", "Unavailable", false, 1, maxRunRequests},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeEC2(t)
			for _, subnet := range testConfig().Subnets {
				f.failures[subnet] = tt.code
			}
			p := newTestProvider(testConfig(), f)

			err := p.Create(t.Context(), testMachine("rungar-x-1", "rungar-x"))
			if err == nil {
				t.Fatal("Create() = nil, want an error")
			}

			if errors.Is(err, errdefs.ErrNoCapacity) {
				t.Errorf("Create() = %v, want not no capacity", err)
			}
			if got := errors.Is(err, errdefs.ErrInvalidArgument); got != tt.invalid {
				t.Errorf("Create() = %v; invalid argument %v, want %v", err, got, tt.invalid)
			}
			if tt.invalid && tt.code != "Unsupported" {
				checkWrapsEC2Error(t, err, tt.code)
			}
			if n := len(slices.Compact(f.runSubnets())); n != tt.subnets {
				t.Errorf("tried %d subnets, want %d", n, tt.subnets)
			}
			if n := len(f.runRequests()); n != tt.requests {
				t.Errorf("sent %d requests, want %d", n, tt.requests)
			}
		})
	}
}

// TestCreateAsksAgainWhenEC2DoesNotSay checks a request whose reply is lost
// is sent again with the same client token, which EC2 answers with the
// instance the first created: the runner gets one instance, and Create
// succeeds.
func TestCreateAsksAgainWhenEC2DoesNotSay(t *testing.T) {
	f := newFakeEC2(t)
	f.lostReplies = 1
	p := newTestProvider(testConfig(), f)

	if err := p.Create(t.Context(), testMachine("rungar-x-1", "rungar-x")); err != nil {
		t.Fatalf("Create() = %v, want the instance the lost reply was for", err)
	}

	if n := f.instanceCount(); n != 1 {
		t.Errorf("created %d instances, want 1", n)
	}
	runs := f.runRequests()
	if len(runs) != 2 || aws.ToString(runs[0].ClientToken) != aws.ToString(runs[1].ClientToken) {
		t.Errorf("sent %d requests, want the first sent again with its client token", len(runs))
	}
}

// TestCreateStopsWhenEC2NeverSays checks a runner whose instance EC2 never
// says it created is not tried in another subnet, which could create a second.
func TestCreateStopsWhenEC2NeverSays(t *testing.T) {
	f := newFakeEC2(t)
	f.lostReplies = maxRunRequests
	p := newTestProvider(testConfig(), f)

	err := p.Create(t.Context(), testMachine("rungar-x-1", "rungar-x"))
	if err == nil {
		t.Fatal("Create() = nil, want an error")
	}
	if errors.Is(err, errdefs.ErrNoCapacity) || errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("Create() = %v, want neither no capacity nor invalid: the instance may exist", err)
	}

	if got := slices.Compact(f.runSubnets()); len(got) != 1 {
		t.Errorf("tried subnets %v, want only the first", got)
	}
	if n := f.instanceCount(); n != 1 {
		t.Errorf("created %d instances, want 1", n)
	}
}

// TestCreateByAnotherProviderGetsItsOwnInstance checks a runner another
// provider in the account left uncertain, in a subnet the two share, gets an
// instance of its own when Rungar creates it again on the second: EC2
// answers a client token it has seen with that token's instance, or refuses
// it with other parameters, so the second provider's requests must not reuse
// the first's token.
func TestCreateByAnotherProviderGetsItsOwnInstance(t *testing.T) {
	tests := []struct {
		name           string
		securityGroups []string
	}{
		{"same parameters", nil},
		{"other parameters", []string{"sg-0a1b2c3d4e5f60718"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeEC2(t)
			first := newTestProvider(testConfig(), f)
			config := testConfig()
			config.SecurityGroups = tt.securityGroups
			second := newTestProvider(config, f)
			spec := testMachine("rungar-x-1", "rungar-x")

			f.lostReplies = maxRunRequests
			if err := first.Create(t.Context(), spec); err == nil {
				t.Fatal("first Create() = nil, want EC2 not saying")
			}
			if err := second.Create(t.Context(), spec); err != nil {
				t.Fatalf("second Create() = %v", err)
			}

			if n := f.instanceCount(); n != 2 {
				t.Errorf("created %d instances, want one for each provider", n)
			}
		})
	}
}

func TestCreateRefusesAnotherTypesRunner(t *testing.T) {
	p := newTestProvider(testConfig(), newFakeEC2(t))

	spec := testMachine("rungar-x-1", "rungar-x")
	spec.Runner = otherRunner{}

	if err := p.Create(t.Context(), spec); !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("Create() = %v, want invalid argument", err)
	}
}

// TestCreateTriesEachInstanceTypeBeforeTheNextSubnet checks the instance
// types are tried in order in a subnet before the next subnet, each with its
// own client token.
func TestCreateTriesEachInstanceTypeBeforeTheNextSubnet(t *testing.T) {
	f := newFakeEC2(t)
	config := testConfig()
	a, b := config.Subnets[0], config.Subnets[1]
	f.failures[a+" m7i.xlarge"] = "InsufficientInstanceCapacity"
	f.failures[a+" m6i.xlarge"] = "Unsupported"
	p := newTestProvider(config, f)

	spec := testMachine("rungar-x-1", "rungar-x")
	runner := testRunner()
	runner.InstanceTypes = provider.OneOrMore{"m7i.xlarge", "m6i.xlarge"}
	spec.Runner = runner

	if err := p.Create(t.Context(), spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	want := []string{a + " m7i.xlarge", a + " m6i.xlarge", b + " m7i.xlarge"}
	if got := f.tried(); !slices.Equal(got, want) {
		t.Errorf("tried %v, want %v", got, want)
	}

	runs := f.runRequests()
	tokens := map[string]bool{}
	for _, in := range runs {
		tokens[aws.ToString(in.ClientToken)] = true
	}
	if len(tokens) != len(runs) {
		t.Errorf("%d client tokens for %d requests, want one each", len(tokens), len(runs))
	}
}

// TestCreateSkipsAnInstanceTypeOutOfQuota checks a type out of quota is not
// tried in the next subnet, which shares the region's quota, while the
// others are.
func TestCreateSkipsAnInstanceTypeOutOfQuota(t *testing.T) {
	f := newFakeEC2(t)
	config := testConfig()
	a, b := config.Subnets[0], config.Subnets[1]
	f.failures[a+" g5.xlarge"] = "VcpuLimitExceeded"
	f.failures[a+" m7i.xlarge"] = "InsufficientInstanceCapacity"
	p := newTestProvider(config, f)

	spec := testMachine("rungar-x-1", "rungar-x")
	runner := testRunner()
	runner.InstanceTypes = provider.OneOrMore{"g5.xlarge", "m7i.xlarge"}
	spec.Runner = runner

	if err := p.Create(t.Context(), spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	want := []string{a + " g5.xlarge", a + " m7i.xlarge", b + " m7i.xlarge"}
	if got := f.tried(); !slices.Equal(got, want) {
		t.Errorf("tried %v, want %v", got, want)
	}
}

func TestCreateIsNoCapacityWhenEveryInstanceTypeIsOutOfQuota(t *testing.T) {
	f := newFakeEC2(t)
	config := testConfig()
	for _, subnet := range config.Subnets {
		f.failures[subnet] = "VcpuLimitExceeded"
	}
	p := newTestProvider(config, f)

	spec := testMachine("rungar-x-1", "rungar-x")
	runner := testRunner()
	runner.InstanceTypes = provider.OneOrMore{"m7i.xlarge", "m6i.xlarge"}
	spec.Runner = runner

	err := p.Create(t.Context(), spec)
	if !errors.Is(err, errdefs.ErrNoCapacity) {
		t.Fatalf("Create() = %v, want no capacity", err)
	}
	if n := len(f.runRequests()); n != 2 {
		t.Errorf("sent %d requests, want one per instance type", n)
	}
}

// TestCreateFromALaunchTemplate checks a runner with a launch template and
// nothing else sends the template, and leaves it the AMI, the instance type
// and the root volume.
func TestCreateFromALaunchTemplate(t *testing.T) {
	f := newFakeEC2(t)
	p := newTestProvider(testConfig(), f)

	spec := testMachine("rungar-x-1", "rungar-x")
	spec.Runner = RunnerSpec{LaunchTemplate: "runner-gpu:3"}.withDefaults()

	if err := p.Create(t.Context(), spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	in := f.lastRun()
	checks := []struct {
		what      string
		got, want any
	}{
		{"template name", aws.ToString(in.LaunchTemplate.LaunchTemplateName), "runner-gpu"},
		{"template ID", in.LaunchTemplate.LaunchTemplateId == nil, true},
		{"template version", aws.ToString(in.LaunchTemplate.Version), "3"},
		{"image", in.ImageId == nil, true},
		{"instance type", string(in.InstanceType), ""},
		{"block devices", len(in.BlockDeviceMappings), 0},
		{"asked for the AMI", f.imageCallCount(), 0},
		{"on shutdown", in.InstanceInitiatedShutdownBehavior, ec2types.ShutdownBehaviorTerminate},
		{"subnet", aws.ToString(in.NetworkInterfaces[0].SubnetId), testConfig().Subnets[0]},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.what, c.got, c.want)
		}
	}
}

// TestCreateSetsTheRootVolumeOverALaunchTemplates checks a runner with a
// launch template, an image and a disk size sets that much of the root
// volume, leaving its type to the template.
func TestCreateSetsTheRootVolumeOverALaunchTemplates(t *testing.T) {
	f := newFakeEC2(t)
	p := newTestProvider(testConfig(), f)

	spec := testMachine("rungar-x-1", "rungar-x")
	spec.Runner = RunnerSpec{
		LaunchTemplate: "lt-0a1b2c3d4e5f60718",
		Image:          "ami-0123456789abcdef0",
		DiskSize:       100 << 30,
	}.withDefaults()

	if err := p.Create(t.Context(), spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	in := f.lastRun()
	if got := aws.ToString(in.LaunchTemplate.LaunchTemplateId); got != "lt-0a1b2c3d4e5f60718" {
		t.Errorf("template ID = %q", got)
	}
	if len(in.BlockDeviceMappings) != 1 {
		t.Fatalf("block devices = %+v, want the root volume", in.BlockDeviceMappings)
	}
	disk := in.BlockDeviceMappings[0]
	if aws.ToString(disk.DeviceName) != "/dev/xvda" || aws.ToInt32(disk.Ebs.VolumeSize) != 100 || disk.Ebs.VolumeType != "" {
		t.Errorf("root volume = %s %+v, want /dev/xvda of 100GiB, of the template's type",
			aws.ToString(disk.DeviceName), *disk.Ebs)
	}
}

// TestCreateSetsTheRootVolumesPerformance checks a runner's IOPS and
// throughput are sent on its root volume.
func TestCreateSetsTheRootVolumesPerformance(t *testing.T) {
	f := newFakeEC2(t)
	p := newTestProvider(testConfig(), f)

	runner := testRunner()
	runner.DiskIOPS = 6000
	runner.DiskThroughput = 500
	spec := testMachine("rungar-x-1", "rungar-x")
	spec.Runner = runner

	if err := p.Create(t.Context(), spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	in := f.lastRun()
	if len(in.BlockDeviceMappings) != 1 {
		t.Fatalf("block devices = %+v, want the root volume", in.BlockDeviceMappings)
	}
	ebs := in.BlockDeviceMappings[0].Ebs
	if aws.ToInt32(ebs.Iops) != 6000 || aws.ToInt32(ebs.Throughput) != 500 || ebs.VolumeType != "gp3" {
		t.Errorf("root volume = %+v, want gp3 of 6000 IOPS and 500MiB/s", *ebs)
	}
}

// TestCreateLeavesTheRootVolumesPerformanceToEC2 checks a runner without IOPS
// or throughput sends neither, so EC2's baseline applies.
func TestCreateLeavesTheRootVolumesPerformanceToEC2(t *testing.T) {
	f := newFakeEC2(t)
	p := newTestProvider(testConfig(), f)

	if err := p.Create(t.Context(), testMachine("rungar-x-1", "rungar-x")); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	ebs := f.lastRun().BlockDeviceMappings[0].Ebs
	if ebs.Iops != nil || ebs.Throughput != nil {
		t.Errorf("root volume = %+v, want no IOPS or throughput", *ebs)
	}
}

// TestCreateSetsTheRootVolumesPerformanceOverALaunchTemplates checks a runner
// with a launch template, an image and a throughput sets only that on the
// root volume, leaving its size, type and IOPS to the template.
func TestCreateSetsTheRootVolumesPerformanceOverALaunchTemplates(t *testing.T) {
	f := newFakeEC2(t)
	p := newTestProvider(testConfig(), f)

	spec := testMachine("rungar-x-1", "rungar-x")
	spec.Runner = RunnerSpec{
		LaunchTemplate: "runner-gpu",
		Image:          "ami-0123456789abcdef0",
		DiskThroughput: 1000,
	}.withDefaults()

	if err := p.Create(t.Context(), spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	in := f.lastRun()
	if len(in.BlockDeviceMappings) != 1 {
		t.Fatalf("block devices = %+v, want the root volume", in.BlockDeviceMappings)
	}
	disk := in.BlockDeviceMappings[0]
	ebs := disk.Ebs
	if aws.ToString(disk.DeviceName) != "/dev/xvda" || aws.ToInt32(ebs.Throughput) != 1000 ||
		ebs.Iops != nil || ebs.VolumeSize != nil || ebs.VolumeType != "" {
		t.Errorf("root volume = %s %+v, want /dev/xvda of 1000MiB/s, the rest the template's",
			aws.ToString(disk.DeviceName), *ebs)
	}
}

// checkWrapsEC2Error checks an error from Create still holds EC2's error of
// code, and says it without the request that failed.
func checkWrapsEC2Error(t *testing.T, err error, code string) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != code {
		t.Errorf("Create() = %v, want it to wrap EC2's %s", err, code)
	}
	if err != nil && strings.Contains(err.Error(), "operation error") {
		t.Errorf("Create() = %q, want EC2's message without the request", err)
	}
}
