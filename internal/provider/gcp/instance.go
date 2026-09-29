// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package gcp

import (
	"encoding/json"
	"maps"
	"path"
	"slices"
	"strings"
	"time"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"

	"github.com/konradasb/rungar/internal/types"
)

const (
	// metadataPrefix starts the metadata keys Rungar sets.
	metadataPrefix = "rungar-"

	// startupScriptKey is the metadata key the guest environment runs at
	// boot.
	startupScriptKey = "startup-script"

	// jitConfigKey is the metadata key holding the runner's just-in-time
	// configuration.
	jitConfigKey = metadataPrefix + "jitconfig"

	// labelsKey is the metadata key holding Rungar's labels, as JSON.
	labelsKey = metadataPrefix + "labels"
)

// instanceFor returns the instance a runner gets in a zone: of machineType,
// or the template's if it is empty, and created from instanceTemplate, the
// properties of the runner's instance template, if it has one. It is never
// restarted, since its registration is good for one use, and deleted by
// Compute Engine when taken back from Spot or at spec's deadline.
//
// What the instance sets, Compute Engine takes over the template's whole: the
// disks, the network interfaces, the labels and the metadata. So with a
// template, the disks are always the template's, the network and service
// account are unless the provider sets them, and the labels, metadata and
// network tags are the template's with the runner's and Rungar's added.
func (c *Config) instanceFor(spec types.MachineSpec, runner RunnerSpec, zone, machineType string,
	instanceTemplate *compute.InstanceProperties,
) (*compute.Instance, error) {
	labelsJSON, err := json.Marshal(spec.Labels)
	if err != nil {
		return nil, err
	}

	labels := map[string]string{}
	metadata := map[string]string{}
	var tags []string
	if instanceTemplate != nil {
		maps.Copy(labels, instanceTemplate.Labels)
		if instanceTemplate.Metadata != nil {
			for _, item := range instanceTemplate.Metadata.Items {
				if item.Value != nil {
					metadata[item.Key] = *item.Value
				}
			}
		}
		if instanceTemplate.Tags != nil {
			tags = append(tags, instanceTemplate.Tags.Items...)
		}
	}

	maps.Copy(labels, runner.Labels)
	maps.Copy(labels, computeLabels(spec.Labels))

	maps.Copy(metadata, runner.Metadata)
	metadata[jitConfigKey] = spec.JITConfig
	metadata[labelsKey] = string(labelsJSON)
	metadata[startupScriptKey] = runner.StartupScript

	tags = append(tags, runner.NetworkTags...)
	slices.Sort(tags)
	tags = slices.Compact(tags)

	instance := &compute.Instance{
		Name:       spec.Name,
		Labels:     labels,
		Metadata:   metadataOf(metadata),
		Scheduling: scheduling(runner.Spot, spec.Deadline, instanceTemplate),
	}

	if machineType != "" {
		instance.MachineType = "zones/" + zone + "/machineTypes/" + machineType
	}

	if instanceTemplate == nil {
		instance.Disks = []*compute.AttachedDisk{{
			Boot:       true,
			AutoDelete: true,
			InitializeParams: &compute.AttachedDiskInitializeParams{
				SourceImage: c.imageURL(runner.Image),
				DiskSizeGb:  runner.DiskSize.Bytes() >> 30,
				DiskType:    "zones/" + zone + "/diskTypes/" + runner.DiskType,
				Labels:      labels,
			},
		}}
	}

	if instanceTemplate == nil || c.setsNetwork() {
		instance.NetworkInterfaces = []*compute.NetworkInterface{c.networkInterface()}
	}

	if len(tags) > 0 {
		instance.Tags = &compute.Tags{Items: tags}
	}

	if c.ServiceAccount != "" {
		instance.ServiceAccounts = []*compute.ServiceAccount{{Email: c.ServiceAccount, Scopes: c.Scopes}}
	}

	return instance, nil
}

// imageURL returns an image as a URL Compute Engine resolves: one given as a
// name is the provider project's.
func (c *Config) imageURL(image string) string {
	return resourceURL(image, "projects/"+c.Project+"/global/images/")
}

// networkInterface returns the instance's network interface, with an external
// address unless external_ip is false.
func (c *Config) networkInterface() *compute.NetworkInterface {
	network := &compute.NetworkInterface{}

	switch {
	case c.Network != "":
		network.Network = resourceURL(c.Network, "global/networks/")
	case c.Subnetwork == "":
		network.Network = "global/networks/default"
	}
	if c.Subnetwork != "" {
		network.Subnetwork = resourceURL(c.Subnetwork, "regions/"+c.region()+"/subnetworks/")
	}

	if c.hasExternalIP() {
		network.AccessConfigs = []*compute.AccessConfig{{Name: "External NAT", Type: "ONE_TO_ONE_NAT"}}
	}

	return network
}

// setsNetwork reports whether the provider says what network an instance is
// on, or whether it has an external address, rather than leaving it to an
// instance template.
func (c *Config) setsNetwork() bool {
	return c.Network != "" || c.Subnetwork != "" || c.ExternalIP != nil
}

// resourceURL returns a resource given by name or URL as a URL, a name being
// under prefix.
func resourceURL(nameOrURL, prefix string) string {
	if strings.Contains(nameOrURL, "/") {
		return nameOrURL
	}

	return prefix + nameOrURL
}

// scheduling returns an instance's scheduling: never restarted, on Spot
// capacity if spot is set or the template is Spot, and deleted by Compute
// Engine at deadline, unless it is zero or the template limits how long its
// instances run itself. Host maintenance live migrates an instance, so its
// job carries on, but stops a Spot one, which cannot migrate. A Spot instance
// taken back is deleted, as at the deadline: the two share one action, and a
// stopped instance would keep its disk until Rungar deleted it.
//
// Compute Engine takes scheduling over a template's field by field, so with
// a template host maintenance is left to it: an instance with a GPU must be
// stopped for maintenance, and only the template knows it has one.
func scheduling(spot bool, deadline time.Time, instanceTemplate *compute.InstanceProperties) *compute.Scheduling {
	s := &compute.Scheduling{AutomaticRestart: googleapi.Bool(false)}

	spot = spot || isSpot(instanceTemplate)
	switch {
	case spot:
		s.ProvisioningModel = "SPOT"
		s.OnHostMaintenance = "TERMINATE"
	case instanceTemplate == nil:
		// An e2 instance must migrate unless it is Spot: Compute Engine
		// refuses one that would be stopped.
		s.OnHostMaintenance = "MIGRATE"
	}

	limited := !deadline.IsZero() && !limitsRunTime(instanceTemplate)
	if limited {
		s.TerminationTime = deadline.UTC().Format(time.RFC3339)
	}

	// Compute Engine refuses a termination action on an instance that is
	// neither Spot nor limited.
	if spot || limited {
		s.InstanceTerminationAction = "DELETE"
	}

	return s
}

// limitsRunTime reports whether a template limits how long its instances run.
func limitsRunTime(instanceTemplate *compute.InstanceProperties) bool {
	return instanceTemplate != nil && instanceTemplate.Scheduling != nil &&
		(instanceTemplate.Scheduling.MaxRunDuration != nil || instanceTemplate.Scheduling.TerminationTime != "")
}

// isSpot reports whether a template creates Spot instances.
func isSpot(instanceTemplate *compute.InstanceProperties) bool {
	return instanceTemplate != nil && instanceTemplate.Scheduling != nil && instanceTemplate.Scheduling.ProvisioningModel == "SPOT"
}

// metadataOf converts a map to instance metadata.
func metadataOf(m map[string]string) *compute.Metadata {
	metadata := &compute.Metadata{}
	for key, value := range m {
		metadata.Items = append(metadata.Items, &compute.MetadataItems{Key: key, Value: googleapi.String(value)})
	}

	return metadata
}

// machineOf converts an instance to a machine. An instance without Rungar's
// labels in its metadata is not one of Rungar's, and ok is false.
func machineOf(instance *compute.Instance) (m types.Machine, ok bool) {
	labels, ok := rungarLabels(instance)
	if !ok {
		return types.Machine{}, false
	}

	m = types.Machine{
		Name:   instance.Name,
		Labels: labels,
		State:  machineState(instance.Status),
		Size:   path.Base(instance.MachineType),
	}

	if created, err := time.Parse(time.RFC3339, instance.CreationTimestamp); err == nil {
		m.CreatedAt = created
	}

	return m, true
}

// rungarLabels returns the Rungar labels an instance's metadata holds.
func rungarLabels(instance *compute.Instance) (map[string]string, bool) {
	if instance.Metadata == nil {
		return nil, false
	}

	for _, item := range instance.Metadata.Items {
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
