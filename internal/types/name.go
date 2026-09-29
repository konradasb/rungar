// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types

import (
	"regexp"
	"strings"

	"github.com/konradasb/rungar/internal/errdefs"
)

// RunnerName returns the name of a scale set's runner: the scale set's name,
// shortened so that it and suffix fit in one DNS label, then suffix. The name
// is both the machine's and the runner's on GitHub, so it must be an RFC 1123
// subdomain, which every backend and GitHub accept.
func RunnerName(scaleSet, suffix string) (string, error) {
	prefix := scaleSet
	if limit := maxDNSLabelLen - len("-") - len(suffix); len(prefix) > limit {
		prefix = strings.TrimRight(prefix[:limit], "-.")
	}

	name := prefix + "-" + suffix
	if !ValidRunnerName(name) {
		return "", errdefs.InvalidArgument("scale set %q does not make a valid runner name %q: use letters, "+
			"digits and hyphens, in dot-separated parts that each start and end with a letter or digit",
			scaleSet, name)
	}

	return name, nil
}

// ValidRunnerName reports whether name is a valid runner name.
func ValidRunnerName(name string) bool {
	return len(name) <= maxNameLen && nameRe.MatchString(name)
}

// nameRe matches an RFC 1123 subdomain.
var nameRe = regexp.MustCompile(
	`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)

// Length limits of an RFC 1123 subdomain, and of each of its labels.
const (
	maxNameLen     = 253
	maxDNSLabelLen = 63
)
