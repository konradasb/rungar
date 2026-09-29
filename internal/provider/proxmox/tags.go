// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package proxmox

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
)

// tagPrefix begins every tag the provider sets.
const tagPrefix = "rungar-"

// tagsOf returns the tags that stand for labels: one per label, a hash of it,
// as Proxmox VE allows few characters in a tag. They are sorted, so that a VM
// is tagged the same whatever the order of its labels.
func tagsOf(labels map[string]string) []string {
	tags := make([]string, 0, len(labels))
	for k, v := range labels {
		tags = append(tags, labelTag(k, v))
	}
	slices.Sort(tags)

	return tags
}

// labelTag returns the tag that stands for one label.
func labelTag(key, value string) string {
	sum := sha256.Sum256([]byte(key + "=" + value))

	return tagPrefix + hex.EncodeToString(sum[:8])
}

// tags returns a VM's tags. Proxmox VE separates them with semicolons, and
// accepts commas and spaces.
func (r resource) tags() []string {
	return strings.FieldsFunc(r.Tags, func(c rune) bool { return c == ';' || c == ',' || c == ' ' })
}

// hasTags reports whether a VM carries every tag in want.
func (r resource) hasTags(want []string) bool {
	have := r.tags()
	for _, tag := range want {
		if !slices.Contains(have, tag) {
			return false
		}
	}

	return true
}
