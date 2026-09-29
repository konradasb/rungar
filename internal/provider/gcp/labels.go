// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package gcp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Rungar's labels do not fit Compute Engine's, which allow only lower case
// letters, digits, _ and - in 63 characters. They are kept whole, as JSON, in
// the instance's metadata, and each is also set as a Compute Engine label
// derived from it, which List filters on.

// labelPrefix starts the keys of the Compute Engine labels Rungar sets.
const labelPrefix = "rungar_"

// maxLabelLen is the most characters a Compute Engine label key or value has.
const maxLabelLen = 63

// gceLabels returns the Compute Engine labels standing for Rungar's.
func gceLabels(labels map[string]string) map[string]string {
	out := make(map[string]string, len(labels))
	for key, value := range labels {
		out[gceLabelKey(key)] = gceLabelValue(value)
	}

	return out
}

// gceLabelKey returns the Compute Engine label key standing for a Rungar label
// key: rungar.sh/scale-set is rungar_sh_scale-set.
func gceLabelKey(key string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(key) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}

	k := b.String()
	if !strings.HasPrefix(k, labelPrefix) {
		k = labelPrefix + k
	}

	return shorten(k, key)
}

// gceLabelValue returns the Compute Engine label value standing for a Rungar
// label value: the value itself if Compute Engine allows it, and a hash of it
// otherwise.
func gceLabelValue(value string) string {
	if gceLabelValuePattern.MatchString(value) {
		return value
	}

	return "h-" + hash(value)[:16]
}

// shorten cuts s to maxLabelLen, ending it with a hash of original so that
// two long keys stay apart.
func shorten(s, original string) string {
	if len(s) <= maxLabelLen {
		return s
	}

	return s[:maxLabelLen-9] + "_" + hash(original)[:8]
}

// hash returns the hex SHA-256 of s.
func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// labelFilter returns a Compute Engine list filter matching the instances
// carrying every label in selector.
func labelFilter(selector map[string]string) string {
	labels := gceLabels(selector)

	terms := make([]string, 0, len(labels))
	for _, key := range slices.Sorted(maps.Keys(labels)) {
		terms = append(terms, fmt.Sprintf("(labels.%s = %q)", key, labels[key]))
	}

	return strings.Join(terms, " ")
}

// nameFilter returns a Compute Engine list filter matching the instance of
// this name.
func nameFilter(name string) string {
	return fmt.Sprintf("(name = %q)", name)
}
