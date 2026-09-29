// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package aws

import (
	"maps"
	"slices"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/konradasb/rungar/internal/types"
)

// Rungar's labels fit EC2's tags as they are, so each is kept as a tag of
// the same key and value, which List and Delete filter on.

const (
	// nameTag is the tag EC2's console shows as an instance's name, and
	// which Delete finds an instance by.
	nameTag = "Name"

	// maxTagKeyLength and maxTagValueLength are the most characters a tag's
	// key and value have.
	maxTagKeyLength   = 128
	maxTagValueLength = 256
)

// tagsOf returns an instance's tags: its name, Rungar's labels as they are,
// and the runner block's.
func tagsOf(spec types.MachineSpec, runner RunnerSpec) []ec2types.Tag {
	m := map[string]string{nameTag: spec.Name}
	maps.Copy(m, runner.Tags)
	maps.Copy(m, spec.Labels)

	tags := make([]ec2types.Tag, 0, len(m))
	for _, key := range slices.Sorted(maps.Keys(m)) {
		tags = append(tags, ec2types.Tag{Key: aws.String(key), Value: aws.String(m[key])})
	}

	return tags
}

// tagValue returns the value of the tag of this key, or "".
func tagValue(tags []ec2types.Tag, key string) string {
	for _, tag := range tags {
		if aws.ToString(tag.Key) == key {
			return aws.ToString(tag.Value)
		}
	}

	return ""
}

// tagFilters returns the filters matching the instances carrying every label
// in selector as a tag.
func tagFilters(selector map[string]string) []ec2types.Filter {
	filters := make([]ec2types.Filter, 0, len(selector))
	for _, key := range slices.Sorted(maps.Keys(selector)) {
		filters = append(filters, ec2types.Filter{Name: aws.String("tag:" + key), Values: []string{selector[key]}})
	}

	return filters
}
