// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package proxmox

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/konradasb/rungar/internal/types"
)

// tagPattern is the pattern Proxmox VE requires of a tag.
var tagPattern = regexp.MustCompile(`^[a-z0-9_][a-z0-9_\-+.]*$`)

func TestLabelsBecomeSortedTagsProxmoxVEAllows(t *testing.T) {
	labels := types.RunnerLabels("gh-4e3651f01be5", "rungar-c2-m4", "rungar-c2-m4-1a2b3c4d", "abc123")

	tags := tagsOf(labels)
	if len(tags) != len(labels) {
		t.Fatalf("tagsOf() = %v, want one tag a label", tags)
	}
	if !slices.IsSorted(tags) {
		t.Errorf("tagsOf() = %v, want them sorted", tags)
	}
	for _, tag := range tags {
		if !tagPattern.MatchString(tag) {
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
