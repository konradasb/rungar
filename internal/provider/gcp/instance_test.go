// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package gcp

import (
	"testing"
	"time"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"

	"github.com/konradasb/rungar/internal/types"
)

func TestInstanceStatusesAreMachineStates(t *testing.T) {
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
		t.Run(tt.status, func(t *testing.T) {
			if got := machineState(tt.status); got != tt.want {
				t.Errorf("machineState(%s) = %s, want %s", tt.status, got, tt.want)
			}
		})
	}
}

// TestInstanceIsDeletedAtItsDeadline checks an instance is deleted at its
// deadline, unless it has none or its template limits how long it runs
// itself, and that only an instance that is Spot or limited has a termination
// action, which Compute Engine refuses on any other.
func TestInstanceIsDeletedAtItsDeadline(t *testing.T) {
	deadline := time.Date(2026, 10, 1, 14, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
	limited := &compute.InstanceProperties{Scheduling: &compute.Scheduling{MaxRunDuration: &compute.Duration{Seconds: 3600}}}

	tests := []struct {
		name             string
		spot             bool
		deadline         time.Time
		instanceTemplate *compute.InstanceProperties
		termination      string
		action           string
	}{
		{name: "no deadline"},
		{name: "no deadline on Spot", spot: true, action: "DELETE"},
		{name: "a deadline", deadline: deadline, termination: "2026-10-01T12:00:00Z", action: "DELETE"},
		{name: "a deadline on Spot", spot: true, deadline: deadline, termination: "2026-10-01T12:00:00Z", action: "DELETE"},
		{name: "a template's own limit", deadline: deadline, instanceTemplate: limited},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := scheduling(tt.spot, tt.deadline, tt.instanceTemplate)
			if s.TerminationTime != tt.termination || s.InstanceTerminationAction != tt.action {
				t.Errorf("scheduling() = termination time %q, action %q; want %q, %q",
					s.TerminationTime, s.InstanceTerminationAction, tt.termination, tt.action)
			}
		})
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
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := machineOf(&compute.Instance{Name: "x", Metadata: tt.metadata}); ok {
				t.Error("machineOf() ok, want an instance that is not Rungar's")
			}
		})
	}
}

func TestImageAndNetworkURLs(t *testing.T) {
	c := testConfig()
	c.Network = "projects/shared/global/networks/ci"
	c.Subnetwork = "projects/shared/regions/europe-west1/subnetworks/runners"

	if got := c.imageURL("projects/debian-cloud/global/images/family/debian-12"); got != "projects/debian-cloud/global/images/family/debian-12" {
		t.Errorf("imageURL() of a URL = %q, want it as it is", got)
	}

	networkInterface := c.networkInterface()
	if networkInterface.Network != c.Network || networkInterface.Subnetwork != c.Subnetwork {
		t.Errorf("networkInterface() = %+v, want the URLs as they are", networkInterface)
	}
}
