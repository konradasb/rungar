// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package version holds the build's identity, set at link time with -X.
package version

import "fmt"

// Build identity. The defaults are those of a build without -X.
var (
	// Version is the released version of Rungar.
	Version = "0.0.0"

	// BuildDate is when the binary was built.
	BuildDate = "1970-01-01T00:00:00Z"

	// Commit is the Git SHA the binary was built from.
	Commit = ""
)

// String describes the build: "0.1.0 (built 2026-09-23T10:00:00Z from commit
// 1a2b3c4)". The commit is left out when Commit is empty.
func String() string {
	if Commit == "" {
		return fmt.Sprintf("%s (built %s)", Version, BuildDate)
	}

	return fmt.Sprintf("%s (built %s from commit %s)", Version, BuildDate, Commit)
}
