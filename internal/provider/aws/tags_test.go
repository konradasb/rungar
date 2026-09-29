// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package aws

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/konradasb/rungar/internal/types"
)

// TestTagsOfNamesTheInstanceAndKeepsRungarsLabels checks an instance is tagged
// with its name, the runner block's tags and Rungar's labels, the labels
// winning over a tag of the same key, in key order.
func TestTagsOfNamesTheInstanceAndKeepsRungarsLabels(t *testing.T) {
	spec := testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8")
	runner := testRunner()
	runner.Tags = map[string]string{"team": "ci", types.LabelScaleSet: "not-rungars"}

	tags := tagsOf(spec, runner)

	if got := tagValue(tags, nameTag); got != spec.Name {
		t.Errorf("Name tag = %q, want %q", got, spec.Name)
	}
	if got := tagValue(tags, "team"); got != "ci" {
		t.Errorf("team tag = %q, want the runner block's", got)
	}
	for key, want := range spec.Labels {
		if got := tagValue(tags, key); got != want {
			t.Errorf("%s tag = %q, want Rungar's %q", key, got, want)
		}
	}
	if len(tags) != len(spec.Labels)+2 {
		t.Errorf("%d tags, want the name, the label and the runner block's one", len(tags))
	}
	for i := 1; i < len(tags); i++ {
		if aws.ToString(tags[i-1].Key) >= aws.ToString(tags[i].Key) {
			t.Errorf("tags not in key order: %s before %s", aws.ToString(tags[i-1].Key), aws.ToString(tags[i].Key))
		}
	}
}

func TestTagValueIsEmptyForATagNotThere(t *testing.T) {
	if got := tagValue(tagsOf(testMachine("rungar-x-1", "rungar-x"), testRunner()), "team"); got != "" {
		t.Errorf("tagValue() = %q, want empty", got)
	}
}

func TestTagFiltersMatchEachLabelInKeyOrder(t *testing.T) {
	filters := tagFilters(map[string]string{"b": "2", "a": "1"})

	want := []struct{ name, value string }{{"tag:a", "1"}, {"tag:b", "2"}}
	if len(filters) != len(want) {
		t.Fatalf("tagFilters() = %d filters, want %d", len(filters), len(want))
	}
	for i, w := range want {
		f := filters[i]
		if aws.ToString(f.Name) != w.name || len(f.Values) != 1 || f.Values[0] != w.value {
			t.Errorf("filter %d = %s %v, want %s [%s]", i, aws.ToString(f.Name), f.Values, w.name, w.value)
		}
	}
}
