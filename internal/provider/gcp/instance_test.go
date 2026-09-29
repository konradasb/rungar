// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package gcp

import (
	"testing"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"

	"github.com/konradasb/rungar/internal/types"
)

func TestMachineState(t *testing.T) {
	tests := []struct {
		status string
		want   types.MachineState
	}{
		{"PROVISIONING", types.MachineStarting},
		{"STAGING", types.MachineStarting},
		{"RUNNING", types.MachineRunning},
		{"STOPPING", types.MachineStopped},
		{"TERMINATED", types.MachineStopped},
		{"SUSPENDED", types.MachineStopped},
		{"REPAIRING", types.MachineStopped},
	}

	for _, tt := range tests {
		if got := machineState(tt.status); got != tt.want {
			t.Errorf("machineState(%s) = %s, want %s", tt.status, got, tt.want)
		}
	}
}

func TestMachineOfNeedsRungarsLabels(t *testing.T) {
	tests := []struct {
		name     string
		metadata *compute.Metadata
	}{
		{"no metadata", nil},
		{"no labels", &compute.Metadata{}},
		{"labels not JSON", &compute.Metadata{Items: []*compute.MetadataItems{
			{Key: labelsKey, Value: googleapi.String("{")},
		}}},
	}

	for _, tt := range tests {
		if _, ok := machineOf(&compute.Instance{Name: "x", Metadata: tt.metadata}); ok {
			t.Errorf("%s: machineOf() ok, want an instance that is not Rungar's", tt.name)
		}
	}
}

func TestImageAndNetworkURLs(t *testing.T) {
	c := testConfig()
	c.Network = "projects/shared/global/networks/ci"
	c.Subnetwork = "projects/shared/regions/europe-west1/subnetworks/runners"

	if got := c.imageURL("projects/debian-cloud/global/images/family/debian-12"); got != "projects/debian-cloud/global/images/family/debian-12" {
		t.Errorf("imageURL() of a URL = %q, want it as it is", got)
	}

	nic := c.networkInterface()
	if nic.Network != c.Network || nic.Subnetwork != c.Subnetwork {
		t.Errorf("networkInterface() = %+v, want the URLs as they are", nic)
	}
}
