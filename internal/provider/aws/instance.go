// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package aws

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"maps"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/konradasb/rungar/internal/types"
)

// jitConfigEnv is the variable the user data exports the runner's
// just-in-time configuration as, which the Actions runner reads.
const jitConfigEnv = "ACTIONS_RUNNER_INPUT_JITCONFIG"

// runInstancesInput returns the request that makes a runner's instance in a
// subnet, from the AMI whose root device is rootDevice. The instance is
// never restarted, since its registration is good for one use: it
// terminates when it shuts down, and a Spot Instance EC2 takes back is
// terminated.
func (c *Config) runInstancesInput(spec types.MachineSpec, runner RunnerSpec, subnet, rootDevice string) *ec2.RunInstancesInput {
	tags := tagsOf(spec, runner)

	in := &ec2.RunInstancesInput{
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		ClientToken:  aws.String(clientToken(spec.Name, subnet)),
		ImageId:      aws.String(runner.Image),
		InstanceType: ec2types.InstanceType(runner.InstanceType),
		UserData:     aws.String(base64.StdEncoding.EncodeToString([]byte(userData(runner.UserData, spec.JITConfig)))),
		NetworkInterfaces: []ec2types.InstanceNetworkInterfaceSpecification{{
			DeviceIndex:              aws.Int32(0),
			SubnetId:                 aws.String(subnet),
			Groups:                   c.SecurityGroups,
			AssociatePublicIpAddress: c.PublicIP,
			DeleteOnTermination:      aws.Bool(true),
		}},
		BlockDeviceMappings: []ec2types.BlockDeviceMapping{{
			DeviceName: aws.String(rootDevice),
			Ebs: &ec2types.EbsBlockDevice{
				VolumeSize:          aws.Int32(int32(runner.DiskSize >> 30)),
				VolumeType:          ec2types.VolumeType(runner.DiskType),
				DeleteOnTermination: aws.Bool(true),
			},
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

// tagsOf returns an instance's tags: its name, Rungar's labels as they are,
// and the runner block's.
func tagsOf(spec types.MachineSpec, runner RunnerSpec) []ec2types.Tag {
	m := map[string]string{nameTag: spec.Name}
	maps.Copy(m, runner.Tags)
	maps.Copy(m, spec.Labels)

	tags := make([]ec2types.Tag, 0, len(m))
	for _, key := range slices.Sorted(maps.Keys(m)) {
		tags = append(tags, ec2types.Tag{Key: aws.String(key), Value: aws.String(m[key])})
	}

	return tags
}

// userData returns a runner's user data script with the registration
// exported after its first line.
func userData(script, jitConfig string) string {
	first, rest, _ := strings.Cut(script, "\n")

	return first + "\nexport " + jitConfigEnv + "=" + shellQuote(jitConfig) + "\n" + rest
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// clientToken returns the idempotency token of a runner's instance in a
// subnet, so that a request the SDK retries makes one instance, and the
// next subnet's request is not taken for a repeat.
func clientToken(name, subnet string) string {
	sum := sha256.Sum256([]byte(name + "/" + subnet))
	return hex.EncodeToString(sum[:16])
}

// instanceProfile returns an instance profile given by name or ARN.
func instanceProfile(nameOrARN string) *ec2types.IamInstanceProfileSpecification {
	if strings.HasPrefix(nameOrARN, "arn:") {
		return &ec2types.IamInstanceProfileSpecification{Arn: aws.String(nameOrARN)}
	}

	return &ec2types.IamInstanceProfileSpecification{Name: aws.String(nameOrARN)}
}

// machineOf converts an instance to a machine: its labels are its tags that
// are Rungar's.
func machineOf(inst ec2types.Instance) types.Machine {
	m := types.Machine{
		Name:   tagValue(inst.Tags, nameTag),
		Labels: map[string]string{},
		Size:   string(inst.InstanceType),
	}

	for _, tag := range inst.Tags {
		key := aws.ToString(tag.Key)
		if strings.HasPrefix(key, labelPrefix) {
			m.Labels[key] = aws.ToString(tag.Value)
		}
	}

	if inst.State != nil {
		m.State = machineState(inst.State.Name)
	}
	if inst.LaunchTime != nil {
		m.CreatedAt = *inst.LaunchTime
	}

	return m
}

// tagValue returns the value of the tag of this key, or "".
func tagValue(tags []ec2types.Tag, key string) string {
	for _, tag := range tags {
		if aws.ToString(tag.Key) == key {
			return aws.ToString(tag.Value)
		}
	}

	return ""
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
