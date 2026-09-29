// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types_test

import (
	"testing"

	"github.com/konradasb/rungar/internal/types"
)

func TestRunnerLabels(t *testing.T) {
	labels := types.RunnerLabels("gh-0123456789ab", "rungar-vm", "rungar-vm-abcd", "0123abcd")

	want := map[string]string{
		types.LabelManaged:      "true",
		types.LabelInstallation: "gh-0123456789ab",
		types.LabelScaleSet:     "rungar-vm",
		types.LabelRunner:       "rungar-vm-abcd",
		types.LabelRevision:     "0123abcd",
	}

	for k, v := range want {
		if labels[k] != v {
			t.Errorf("label %s = %q, want %q", k, labels[k], v)
		}
	}
}

// TestScaleSetSelector checks the one property adoption rests on: a scale
// set's selector finds its own runners and nobody else's.
func TestScaleSetSelector(t *testing.T) {
	ours := types.RunnerLabels("rungar-a", "rungar-vm", "rungar-vm-abcd1234", "")

	tests := []struct {
		name   string
		labels map[string]string
		want   bool
	}{
		{name: "rungar's own", labels: ours, want: true},
		{name: "another scale set's", labels: types.RunnerLabels("rungar-a", "other", "other-1", "")},
		{
			// Two installations serving different GitHub accounts may use
			// the same scale set name on the same fleet.
			name:   "another installation's, of the same name",
			labels: types.RunnerLabels("rungar-b", "rungar-vm", "rungar-vm-abcd1234", ""),
		},
		{name: "somebody else's machine", labels: map[string]string{"app": "web"}},
		{name: "no labels at all"},
	}

	selector := types.ScaleSetSelector("rungar-a", "rungar-vm")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := types.Matches(tt.labels, selector); got != tt.want {
				t.Errorf("Matches() = %v, want %v", got, tt.want)
			}
		})
	}
}
