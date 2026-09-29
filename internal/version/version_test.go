// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package version_test

import (
	"testing"

	"github.com/konradasb/rungar/internal/version"
)

// TestStringNamesCommitOnlyWhenSet checks the build is described with its
// version and date, and its commit only when one was stamped in.
func TestStringNamesCommitOnlyWhenSet(t *testing.T) {
	saved := version.Commit
	t.Cleanup(func() { version.Commit = saved })

	for _, tc := range []struct {
		name, commit, want string
	}{
		{"with commit", "1a2b3c4", version.Version + " (built " + version.BuildDate + " from commit 1a2b3c4)"},
		{"without commit", "", version.Version + " (built " + version.BuildDate + ")"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			version.Commit = tc.commit

			if got := version.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}
