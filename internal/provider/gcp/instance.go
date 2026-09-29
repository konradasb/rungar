// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package gcp

import (
	"encoding/json"
	"maps"
	"path"
	"strings"
	"time"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"

	"github.com/konradasb/rungar/internal/types"
)

const (
	// jitConfigKey is the metadata key holding the runner's just-in-time
	// configuration.
	jitConfigKey = metadataPrefix + "jitconfig"

	// labelsKey is the metadata key holding Rungar's labels, as JSON.
	labelsKey = metadataPrefix + "labels"
)

// instanceFor returns the instance a runner gets in a zone. It is never
// restarted, since its registration is good for one use; a Spot instance
// Compute Engine takes back is deleted.
func (c *Config) instanceFor(spec types.MachineSpec, runner RunnerSpec, zone string) (*compute.Instance, error) {
	labelsJSON, err := json.Marshal(spec.Labels)
	if err != nil {
		return nil, err
	}

	labels := gceLabels(spec.Labels)
	maps.Copy(labels, runner.Labels)

	metadata := map[string]string{
		jitConfigKey:     spec.JITConfig,
		labelsKey:        string(labelsJSON),
		startupScriptKey: runner.StartupScript,
	}
	maps.Copy(metadata, runner.Metadata)

	inst := &compute.Instance{
		Name:        spec.Name,
		MachineType: "zones/" + zone + "/machineTypes/" + runner.MachineType,
		Labels:      labels,
		Metadata:    metadataOf(metadata),
		Disks: []*compute.AttachedDisk{{
			Boot:       true,
			AutoDelete: true,
			InitializeParams: &compute.AttachedDiskInitializeParams{
				SourceImage: c.imageURL(runner.Image),
				DiskSizeGb:  runner.DiskSize.Bytes() >> 30,
				DiskType:    "zones/" + zone + "/diskTypes/" + runner.DiskType,
				Labels:      labels,
			},
		}},
		NetworkInterfaces: []*compute.NetworkInterface{c.networkInterface()},
		Scheduling:        scheduling(runner.Spot),
	}

	if len(runner.NetworkTags) > 0 {
		inst.Tags = &compute.Tags{Items: runner.NetworkTags}
	}

	if c.ServiceAccount != "" {
		inst.ServiceAccounts = []*compute.ServiceAccount{{Email: c.ServiceAccount, Scopes: c.Scopes}}
	}

	return inst, nil
}

// imageURL returns an image as a URL Compute Engine resolves: one given as a
// name is the provider project's.
func (c *Config) imageURL(image string) string {
	if strings.Contains(image, "/") {
		return image
	}

	return "projects/" + c.Project + "/global/images/" + image
}

// networkInterface returns the instance's network interface, with an external
// address unless external_ip is false.
func (c *Config) networkInterface() *compute.NetworkInterface {
	nic := &compute.NetworkInterface{}

	switch {
	case c.Network != "":
		nic.Network = c.resourceURL(c.Network, "global/networks/")
	case c.Subnetwork == "":
		nic.Network = "global/networks/default"
	}
	if c.Subnetwork != "" {
		nic.Subnetwork = c.resourceURL(c.Subnetwork, "regions/"+c.region()+"/subnetworks/")
	}

	if c.externalIP() {
		nic.AccessConfigs = []*compute.AccessConfig{{Name: "External NAT", Type: "ONE_TO_ONE_NAT"}}
	}

	return nic
}

// resourceURL returns a resource given by name or URL as a URL, a name being
// under prefix.
func (c *Config) resourceURL(nameOrURL, prefix string) string {
	if strings.Contains(nameOrURL, "/") {
		return nameOrURL
	}

	return prefix + nameOrURL
}

// scheduling returns an instance's scheduling: never restarted, and on Spot
// capacity if spot is set.
func scheduling(spot bool) *compute.Scheduling {
	s := &compute.Scheduling{AutomaticRestart: googleapi.Bool(false), OnHostMaintenance: "TERMINATE"}
	if spot {
		s.ProvisioningModel = "SPOT"
		s.InstanceTerminationAction = "DELETE"
	}

	return s
}

// metadataOf converts a map to instance metadata.
func metadataOf(m map[string]string) *compute.Metadata {
	md := &compute.Metadata{}
	for key, value := range m {
		md.Items = append(md.Items, &compute.MetadataItems{Key: key, Value: googleapi.String(value)})
	}

	return md
}

// machineOf converts an instance to a machine. An instance without Rungar's
// labels in its metadata is not one of Rungar's, and ok is false.
func machineOf(inst *compute.Instance) (m types.Machine, ok bool) {
	labels, ok := rungarLabels(inst)
	if !ok {
		return types.Machine{}, false
	}

	m = types.Machine{
		Name:   inst.Name,
		Labels: labels,
		State:  machineState(inst.Status),
		Size:   path.Base(inst.MachineType),
	}

	if created, err := time.Parse(time.RFC3339, inst.CreationTimestamp); err == nil {
		m.CreatedAt = created
	}

	return m, true
}

// rungarLabels returns the Rungar labels an instance's metadata holds.
func rungarLabels(inst *compute.Instance) (map[string]string, bool) {
	if inst.Metadata == nil {
		return nil, false
	}

	for _, item := range inst.Metadata.Items {
		if item.Key != labelsKey || item.Value == nil {
			continue
		}

		var labels map[string]string
		if err := json.Unmarshal([]byte(*item.Value), &labels); err != nil {
			return nil, false
		}

		return labels, true
	}

	return nil, false
}

// machineState converts an instance's status to a machine's. An instance
// stopping has finished its job, so it counts as stopped.
func machineState(status string) types.MachineState {
	switch status {
	case "PROVISIONING", "STAGING":
		return types.MachineStarting
	case "RUNNING":
		return types.MachineRunning
	default:
		return types.MachineStopped
	}
}
