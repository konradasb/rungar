// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package gcp

import (
	"strings"
	"testing"

	"github.com/konradasb/rungar/internal/types"
)

func TestGCELabelKey(t *testing.T) {
	tests := []struct {
		key, want string
	}{
		{types.LabelManaged, "rungar_sh_managed"},
		{types.LabelScaleSet, "rungar_sh_scale-set"},
		{"Team/Name", "rungar_team_name"},
		{"9lives", "rungar_9lives"},
	}

	for _, tt := range tests {
		if got := gceLabelKey(tt.key); got != tt.want {
			t.Errorf("gceLabelKey(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestLongGCELabelKeysStayApart(t *testing.T) {
	long := strings.Repeat("a", 80)

	a, b := gceLabelKey(long+"1"), gceLabelKey(long+"2")
	for _, k := range []string{a, b} {
		if !gceLabelKeyPattern.MatchString(k) {
			t.Errorf("gceLabelKey() = %q, which Compute Engine does not allow", k)
		}
	}
	if a == b {
		t.Errorf("two long keys both map to %q", a)
	}
	if gceLabelKey(long+"1") != a {
		t.Error("gceLabelKey() is not deterministic")
	}
}

func TestGCELabelValue(t *testing.T) {
	tests := []struct {
		value  string
		hashed bool
	}{
		{"true", false},
		{"rungar-c4-m8", false},
		{"", false},
		{"Rungar-C4", true},
		{"a.b", true},
		{strings.Repeat("a", 64), true},
	}

	for _, tt := range tests {
		got := gceLabelValue(tt.value)
		if !gceLabelValuePattern.MatchString(got) {
			t.Errorf("gceLabelValue(%q) = %q, which Compute Engine does not allow", tt.value, got)
		}
		if hashed := got != tt.value; hashed != tt.hashed {
			t.Errorf("gceLabelValue(%q) = %q, hashed %v, want %v", tt.value, got, hashed, tt.hashed)
		}
	}
}

func TestLabelFilter(t *testing.T) {
	got := labelFilter(map[string]string{types.LabelScaleSet: "Big", types.LabelManaged: "true"})

	want := `(labels.rungar_sh_managed = "true") (labels.rungar_sh_scale-set = "` + gceLabelValue("Big") + `")`
	if got != want {
		t.Errorf("labelFilter() = %s, want %s", got, want)
	}
}
