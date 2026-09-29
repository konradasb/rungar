// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package gcp

import (
	"strings"
	"testing"

	"github.com/konradasb/rungar/internal/types"
)

func TestLabelKeysBecomeComputeEngineKeys(t *testing.T) {
	tests := []struct {
		key, want string
	}{
		{types.LabelManaged, "rungar_sh_managed"},
		{types.LabelScaleSet, "rungar_sh_scale-set"},
		{"Team/Name", "rungar_team_name"},
		{"9lives", "rungar_9lives"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got := computeLabelKey(tt.key); got != tt.want {
				t.Errorf("computeLabelKey(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestLongComputeLabelKeysStayApart(t *testing.T) {
	long := strings.Repeat("a", 80)

	a, b := computeLabelKey(long+"1"), computeLabelKey(long+"2")
	for _, k := range []string{a, b} {
		if !computeLabelKeyPattern.MatchString(k) {
			t.Errorf("computeLabelKey() = %q, which Compute Engine does not allow", k)
		}
	}
	if a == b {
		t.Errorf("two long keys both map to %q", a)
	}
	if computeLabelKey(long+"1") != a {
		t.Error("computeLabelKey() is not deterministic")
	}
}

func TestDisallowedLabelValuesAreHashed(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		hashed bool
	}{
		{"allowed", "true", false},
		{"allowed with hyphens", "rungar-c4-m8", false},
		{"empty", "", false},
		{"upper case", "Rungar-C4", true},
		{"a dot", "a.b", true},
		{"too long", strings.Repeat("a", 64), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeLabelValue(tt.value)
			if !computeLabelValuePattern.MatchString(got) {
				t.Errorf("computeLabelValue(%q) = %q, which Compute Engine does not allow", tt.value, got)
			}
			if hashed := got != tt.value; hashed != tt.hashed {
				t.Errorf("computeLabelValue(%q) = %q, hashed %v, want %v", tt.value, got, hashed, tt.hashed)
			}
		})
	}
}

func TestLabelFilterMatchesEveryLabel(t *testing.T) {
	got := labelFilter(map[string]string{types.LabelScaleSet: "Big", types.LabelManaged: "true"})

	want := `(labels.rungar_sh_managed = "true") (labels.rungar_sh_scale-set = "` + computeLabelValue("Big") + `")`
	if got != want {
		t.Errorf("labelFilter() = %s, want %s", got, want)
	}
}
