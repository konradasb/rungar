// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package proxmox

import (
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/types"
)

// pveTag is the pattern Proxmox VE requires of a tag.
var pveTag = regexp.MustCompile(`^[a-z0-9_][a-z0-9_\-+.]*$`)

func TestTagsOf(t *testing.T) {
	labels := types.RunnerLabels("gh-4e3651f01be5", "rungar-c2-m4", "rungar-c2-m4-1a2b3c4d", "abc123")

	tags := tagsOf(labels)
	if len(tags) != len(labels) {
		t.Fatalf("tagsOf() = %v, want one tag a label", tags)
	}
	if !slices.IsSorted(tags) {
		t.Errorf("tagsOf() = %v, want them sorted", tags)
	}
	for _, tag := range tags {
		if !pveTag.MatchString(tag) {
			t.Errorf("tag %q is not one Proxmox VE allows", tag)
		}
	}

	// A selector's tags are among the labels'.
	vm := resource{Tags: "template;" + strings.Join(tags, ";")}
	if !vm.hasTags(tagsOf(types.ScaleSetSelector("gh-4e3651f01be5", "rungar-c2-m4"))) {
		t.Error("the runner's VM lacks its scale set's tags")
	}
	if vm.hasTags(tagsOf(types.ScaleSetSelector("gh-4e3651f01be5", "rungar-c4-m8"))) {
		t.Error("the runner's VM has another scale set's tags")
	}
}

func TestDescriptionRoundTrips(t *testing.T) {
	labels := types.RunnerLabels("gh-test", "a", "rungar-a", "rev")
	created := time.Date(2026, 9, 29, 10, 15, 2, 500, time.UTC)

	got, at, ok := parseDescription(describeVM(labels, created))
	if !ok || !maps.Equal(got, labels) || !at.Equal(created.Truncate(time.Second)) {
		t.Errorf("parseDescription() = %v, %v, %v; want %v, %v", got, at, ok, labels, created)
	}

	for _, description := range []string{"", "notes about this VM", `{"other":1}`} {
		if _, _, ok := parseDescription(description); ok {
			t.Errorf("parseDescription(%q) = ok, want not Rungar's", description)
		}
	}
}

func TestMachineState(t *testing.T) {
	tests := []struct {
		vm   resource
		want types.MachineState
	}{
		{resource{Status: "running"}, types.MachineRunning},
		{resource{Status: "paused"}, types.MachineRunning},
		{resource{Status: "stopped"}, types.MachineStopped},
		{resource{Status: "stopped", Lock: "clone"}, types.MachineStarting},
		{resource{Status: "stopped", Lock: "create"}, types.MachineStarting},
		{resource{Status: "unknown"}, types.MachineStopped},
	}

	for _, tt := range tests {
		if got := machineState(tt.vm); got != tt.want {
			t.Errorf("machineState(%+v) = %q, want %q", tt.vm, got, tt.want)
		}
	}
}
