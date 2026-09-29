// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"strings"
	"testing"

	ghscaleset "github.com/actions/scaleset"

	"github.com/konradasb/rungar/internal/types"
)

// ensure finds or creates a scale set of spec on api, as starting it does.
func ensure(api *fakeGitHub, spec types.ScaleSetSpec) (int, error) {
	if spec.RunnerGroup == "" {
		spec.RunnerGroup = types.DefaultRunnerGroup
	}

	s := newScaleSetOver(newFakeScaleSetProviders(), api, spec, &fakeMetrics{}, discardLogger())

	return s.ensureOnGitHub(context.Background())
}

// TestEnsureOnGitHubUsesTheScaleSetGitHubHas is the behaviour a restart
// depends on: a scale set carries the jobs assigned to it, so creating a
// second one would strand them.
func TestEnsureOnGitHubUsesTheScaleSetGitHubHas(t *testing.T) {
	api := &fakeGitHub{
		sets: map[string]*ghscaleset.RunnerScaleSet{
			"rungar-vm": {ID: 7, Name: "rungar-vm", Labels: []ghscaleset.Label{{Name: "rungar-vm"}}},
		},
	}

	spec := types.ScaleSetSpec{Name: "rungar-vm", MaxRunners: 4}

	id, err := ensure(api, spec)
	if err != nil {
		t.Fatalf("ensureOnGitHub() = %v", err)
	}

	if id != 7 {
		t.Errorf("used scale set %d, want 7", id)
	}
	if api.created != nil {
		t.Error("a second scale set was created although GitHub already had one")
	}
}

// TestEnsureOnGitHubUsesTheScaleSetWhateverItsLabels checks that a scale set
// whose labels differ from the configured ones is still used and served: GitHub
// will not change them, and its jobs are still worth running.
func TestEnsureOnGitHubUsesTheScaleSetWhateverItsLabels(t *testing.T) {
	api := &fakeGitHub{
		sets: map[string]*ghscaleset.RunnerScaleSet{
			"rungar-vm": {ID: 7, Name: "rungar-vm", Labels: []ghscaleset.Label{{Name: "rungar-vm"}}},
		},
	}

	spec := types.ScaleSetSpec{Name: "rungar-vm", MaxRunners: 4, Labels: []string{"linux"}}

	id, err := ensure(api, spec)
	if err != nil || id != 7 {
		t.Errorf("ensureOnGitHub() = %v, %v; want the scale set GitHub has", id, err)
	}
	if api.created != nil {
		t.Error("a second scale set was created over a difference in labels")
	}
}

func TestSameLabelsIgnoresOrderAndCase(t *testing.T) {
	have := []string{"rungar-vm", "Linux"}

	for _, tt := range []struct {
		name string
		want []string
		same bool
	}{
		{name: "same labels in another case", want: []string{"rungar-vm", "linux"}, same: true},
		{name: "same labels in another order", want: []string{"linux", "rungar-vm"}, same: true},
		{name: "one fewer", want: []string{"rungar-vm"}},
		{name: "one more", want: []string{"rungar-vm", "linux", "x64"}},
		{name: "one different", want: []string{"rungar-vm", "arm64"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := sameLabels(have, tt.want); got != tt.same {
				t.Errorf("sameLabels(%v, %v) = %v, want %v", have, tt.want, got, tt.same)
			}
		})
	}
}

func TestEnsureOnGitHubCreates(t *testing.T) {
	api := &fakeGitHub{}

	spec := types.ScaleSetSpec{
		Name:       "rungar-vm",
		Labels:     []string{"linux", "x64"},
		MaxRunners: 4,
	}

	id, err := ensure(api, spec)
	if err != nil {
		t.Fatalf("ensureOnGitHub() = %v", err)
	}

	if id != 42 {
		t.Errorf("created scale set %d, want the one GitHub returned", id)
	}
	if api.created == nil {
		t.Fatal("nothing was created")
	}
	if api.created.RunnerGroupID != defaultRunnerGroupID {
		t.Errorf("group = %d, want the default group %d",
			api.created.RunnerGroupID, defaultRunnerGroupID)
	}

	var labels []string
	for _, l := range api.created.Labels {
		labels = append(labels, l.Name)
	}
	if len(labels) != 3 || labels[0] != "rungar-vm" {
		t.Errorf("labels = %v, want the name first then the extras", labels)
	}

	// An ephemeral runner lives for one job, so updating itself first is
	// wasted time; the image is where its version comes from.
	if !api.created.RunnerSetting.DisableUpdate {
		t.Error("runner self-update was left on")
	}
}

func TestEnsureOnGitHubLooksUpANamedGroup(t *testing.T) {
	api := &fakeGitHub{groups: map[string]int{"vms": 9}}

	spec := types.ScaleSetSpec{
		Name:        "rungar-vm",
		RunnerGroup: "vms",
		MaxRunners:  4,
	}

	if _, err := ensure(api, spec); err != nil {
		t.Fatalf("ensureOnGitHub() = %v", err)
	}

	if api.created.RunnerGroupID != 9 {
		t.Errorf("group = %d, want the looked-up group 9", api.created.RunnerGroupID)
	}
}

func TestEnsureOnGitHubReportsFailures(t *testing.T) {
	tests := []struct {
		name string
		api  *fakeGitHub
		set  types.ScaleSetSpec
		says string
	}{
		{
			name: "the group cannot be found",
			api:  &fakeGitHub{groupErr: errors.New("nope")},
			set:  types.ScaleSetSpec{Name: "rungar-vm", RunnerGroup: "vms", MaxRunners: 1},
			says: "runner group",
		},
		{
			name: "the lookup fails",
			api:  &fakeGitHub{getErr: errors.New("nope")},
			set:  types.ScaleSetSpec{Name: "rungar-vm", MaxRunners: 1},
			says: "look up scale set",
		},
		{
			name: "the create fails",
			api:  &fakeGitHub{createErr: errors.New("nope")},
			set:  types.ScaleSetSpec{Name: "rungar-vm", MaxRunners: 1},
			says: "create scale set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ensure(tt.api, tt.set)
			if err == nil {
				t.Fatal("ensureOnGitHub() = nil, want an error")
			}
			if !strings.Contains(err.Error(), tt.says) {
				t.Errorf("error %q does not mention %q", err, tt.says)
			}
		})
	}
}
