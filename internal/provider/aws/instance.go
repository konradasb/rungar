// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package aws

import (
	"encoding/base64"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/konradasb/rungar/internal/types"
)

// machineOf converts an instance to a machine: its labels are its tags that
// are Rungar's.
func machineOf(instance ec2types.Instance) types.Machine {
	m := types.Machine{
		Name:   tagValue(instance.Tags, nameTag),
		Labels: map[string]string{},
		Size:   string(instance.InstanceType),
	}

	for _, tag := range instance.Tags {
		key := aws.ToString(tag.Key)
		if strings.HasPrefix(key, types.LabelPrefix) {
			m.Labels[key] = aws.ToString(tag.Value)
		}
	}

	if instance.State != nil {
		m.State = machineState(instance.State.Name)
	}
	if instance.LaunchTime != nil {
		m.CreatedAt = *instance.LaunchTime
	}

	return m
}

// machineState converts an instance's state to a machine's. An instance
// stopping has finished its job, so it counts as stopped.
func machineState(state ec2types.InstanceStateName) types.MachineState {
	switch state {
	case ec2types.InstanceStateNamePending:
		return types.MachineStarting
	case ec2types.InstanceStateNameRunning:
		return types.MachineRunning
	default:
		return types.MachineStopped
	}
}

// runInstancesInput returns the request, with clientToken, that creates a
// runner's instance as try says, from the AMI whose root device is rootDeviceName if the runner
// sets the root volume. The instance is never restarted, since its
// registration is good for one use: it terminates when it shuts down, and a
// Spot Instance EC2 takes back is terminated.
//
// What the request sets is used over the runner's launch template, if it has
// one: tags and block devices key by key, the rest whole. So the template's
// tags and other volumes are kept.
func (c *Config) runInstancesInput(spec types.MachineSpec, runner RunnerSpec, try attempt,
	rootDeviceName, clientToken string,
) *ec2.RunInstancesInput {
	tags := tagsOf(spec, runner)

	in := &ec2.RunInstancesInput{
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		ClientToken:  aws.String(clientToken),
		InstanceType: ec2types.InstanceType(try.instanceType),
		UserData:     aws.String(base64.StdEncoding.EncodeToString([]byte(userData(runner.UserData, spec.JITConfig)))),
		NetworkInterfaces: []ec2types.InstanceNetworkInterfaceSpecification{{
			DeviceIndex:              aws.Int32(0),
			SubnetId:                 aws.String(try.subnet),
			Groups:                   c.SecurityGroups,
			AssociatePublicIpAddress: c.PublicIP,
			DeleteOnTermination:      aws.Bool(true),
		}},
		InstanceInitiatedShutdownBehavior: ec2types.ShutdownBehaviorTerminate,
		MetadataOptions: &ec2types.InstanceMetadataOptionsRequest{
			HttpEndpoint: ec2types.InstanceMetadataEndpointStateEnabled,
			HttpTokens:   ec2types.HttpTokensStateRequired,
		},
		TagSpecifications: []ec2types.TagSpecification{
			{ResourceType: ec2types.ResourceTypeInstance, Tags: tags},
			{ResourceType: ec2types.ResourceTypeVolume, Tags: tags},
		},
	}

	if runner.Image != "" {
		in.ImageId = aws.String(runner.Image)
	}

	if runner.setsRootVolume() {
		ebs := &ec2types.EbsBlockDevice{
			VolumeType:          ec2types.VolumeType(runner.DiskType),
			DeleteOnTermination: aws.Bool(true),
		}
		if runner.DiskSize != 0 {
			ebs.VolumeSize = aws.Int32(int32(runner.DiskSize >> 30))
		}
		if runner.DiskIOPS != 0 {
			ebs.Iops = aws.Int32(runner.DiskIOPS)
		}
		if runner.DiskThroughput != 0 {
			ebs.Throughput = aws.Int32(runner.DiskThroughput)
		}
		in.BlockDeviceMappings = []ec2types.BlockDeviceMapping{{DeviceName: aws.String(rootDeviceName), Ebs: ebs}}
	}

	if runner.LaunchTemplate != "" {
		ref, _ := parseLaunchTemplateRef(runner.LaunchTemplate)
		in.LaunchTemplate = ref.specification()
	}

	if c.InstanceProfile != "" {
		in.IamInstanceProfile = instanceProfile(c.InstanceProfile)
	}

	if runner.Spot {
		in.InstanceMarketOptions = &ec2types.InstanceMarketOptionsRequest{
			MarketType: ec2types.MarketTypeSpot,
			SpotOptions: &ec2types.SpotMarketOptions{
				SpotInstanceType:             ec2types.SpotInstanceTypeOneTime,
				InstanceInterruptionBehavior: ec2types.InstanceInterruptionBehaviorTerminate,
			},
		}
	}

	return in
}

// userData returns a runner's user data script with the registration
// exported after its first line.
func userData(script, jitConfig string) string {
	first, rest, _ := strings.Cut(script, "\n")

	return first + "\nexport " + types.JITConfigEnv + "=" + shellQuote(jitConfig) + "\n" + rest
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// instanceProfile returns an instance profile given by name or ARN.
func instanceProfile(nameOrARN string) *ec2types.IamInstanceProfileSpecification {
	if strings.HasPrefix(nameOrARN, "arn:") {
		return &ec2types.IamInstanceProfileSpecification{Arn: aws.String(nameOrARN)}
	}

	return &ec2types.IamInstanceProfileSpecification{Name: aws.String(nameOrARN)}
}
