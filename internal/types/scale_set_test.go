// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

func TestScaleSetLabelsLeadWithTheNameWithoutRepeats(t *testing.T) {
	tests := []struct {
		name string
		set  types.ScaleSetSpec
		want []string
	}{
		{
			name: "the name is always a label",
			set:  types.ScaleSetSpec{Name: "rungar-vm"},
			want: []string{"rungar-vm"},
		},
		{
			name: "the name comes first",
			set:  types.ScaleSetSpec{Name: "rungar-vm", Labels: []string{"linux", "x64"}},
			want: []string{"rungar-vm", "linux", "x64"},
		},
		{
			name: "a label repeating the name is not repeated",
			set:  types.ScaleSetSpec{Name: "rungar-vm", Labels: []string{"rungar-vm", "linux", "linux"}},
			want: []string{"rungar-vm", "linux"},
		},
		{
			name: "labels differing only in case are one, as on GitHub",
			set:  types.ScaleSetSpec{Name: "rungar-vm", Labels: []string{"Rungar-VM", "Linux", "linux"}},
			want: []string{"rungar-vm", "Linux"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.set.AllLabels(); !slices.Equal(got, tt.want) {
				t.Errorf("AllLabels() = %v, want %v", got, tt.want)
			}
		})
	}
}

// validScaleSet is a scale set as loading leaves it, with change applied.
func validScaleSet(change func(*types.ScaleSetSpec)) types.ScaleSetSpec {
	set := types.ScaleSetSpec{
		Name: "rungar-vm", MaxRunners: 10, Providers: []types.ProviderRef{{Name: "rack1"}},
		Placement: types.PlacementSpread, StartTimeout: time.Minute,
	}
	if change != nil {
		change(&set)
	}

	return set
}

// TestInvalidScaleSetIsRefused checks Validate refuses a scale set missing what
// it needs or with a setting out of range, as an invalid argument.
func TestInvalidScaleSetIsRefused(t *testing.T) {
	tests := []struct {
		name string
		set  types.ScaleSetSpec
		ok   bool
	}{
		{name: "valid", set: validScaleSet(nil), ok: true},
		{name: "no name", set: validScaleSet(func(s *types.ScaleSetSpec) { s.Name = "" })},
		{name: "a name with a space is not a label", set: validScaleSet(func(s *types.ScaleSetSpec) { s.Name = "dicer vm" })},
		{name: "no ceiling", set: validScaleSet(func(s *types.ScaleSetSpec) { s.MaxRunners = 0 })},
		{name: "more reserved than allowed", set: validScaleSet(func(s *types.ScaleSetSpec) { s.MinRunners = 11 })},
		{name: "no provider", set: validScaleSet(func(s *types.ScaleSetSpec) { s.Providers = nil })},
		{
			name: "a provider listed twice",
			set: validScaleSet(func(s *types.ScaleSetSpec) {
				s.Providers = []types.ProviderRef{{Name: "a"}, {Name: "a"}}
			}),
		},
		{name: "an unknown placement", set: validScaleSet(func(s *types.ScaleSetSpec) { s.Placement = "random" })},
		{name: "a negative start timeout", set: validScaleSet(func(s *types.ScaleSetSpec) { s.StartTimeout = -time.Second })},
		{
			name: "a start timeout no runner could boot in",
			set:  validScaleSet(func(s *types.ScaleSetSpec) { s.StartTimeout = time.Millisecond }),
		},
		{name: "a max idle age", set: validScaleSet(func(s *types.ScaleSetSpec) { s.MaxIdleAge = time.Hour }), ok: true},
		{
			name: "a max idle age within the start timeout",
			set:  validScaleSet(func(s *types.ScaleSetSpec) { s.MaxIdleAge = 30 * time.Second }),
		},
		{name: "a negative max idle age", set: validScaleSet(func(s *types.ScaleSetSpec) { s.MaxIdleAge = -time.Hour })},
		{name: "a max age", set: validScaleSet(func(s *types.ScaleSetSpec) { s.MaxAge = 24 * time.Hour }), ok: true},
		{
			name: "a max age within the start timeout",
			set:  validScaleSet(func(s *types.ScaleSetSpec) { s.MaxAge = 30 * time.Second }),
		},
		{
			name: "a name no runner can be named after",
			set:  validScaleSet(func(s *types.ScaleSetSpec) { s.Name = "Rungar_Probe" }),
		},
		{
			name: "a name too long for a runner is shortened, not refused",
			set:  validScaleSet(func(s *types.ScaleSetSpec) { s.Name = strings.Repeat("a", 80) }),
			ok:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.set.Validate()
			if tt.ok {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Validate() = nil, want an error")
			}
			if !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("Validate() = %v, want an ErrInvalidArgument", err)
			}
		})
	}
}

// TestProviderRefIsNameOrMapping checks a scale set's provider may be written
// as its name, or as a mapping with a runner block of its own.
func TestProviderRefIsNameOrMapping(t *testing.T) {
	var set types.ScaleSetSpec
	err := yaml.Unmarshal([]byte(`
providers:
  - compute1
  - name: cloud
    runner: {flavor: m1.large}
`), &set)
	if err != nil {
		t.Fatal(err)
	}

	if got := set.ProviderNames(); !slices.Equal(got, []string{"compute1", "cloud"}) {
		t.Errorf("ProviderNames() = %v", got)
	}
	if set.Providers[1].RunnerBlock.Kind != yaml.MappingNode {
		t.Error("the cloud's own runner block was not kept")
	}
}

// TestRunsOnQuotesWhatYAMLWouldMisread checks the runs-on line is YAML a
// workflow can take as it is, for a name YAML would otherwise read as something
// else.
func TestRunsOnQuotesWhatYAMLWouldMisread(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"rungar-c2-m4", "runs-on: rungar-c2-m4"},
		{"gpu:a100", "runs-on: gpu:a100"},
		{"*gpu", `runs-on: '*gpu'`},
		{"yes", `runs-on: "yes"`},
		{"1234", `runs-on: "1234"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (types.ScaleSetSpec{Name: tt.name}).RunsOn(); got != tt.want {
				t.Errorf("RunsOn() = %s, want %s", got, tt.want)
			}
		})
	}
}
