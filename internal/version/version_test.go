// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package version_test

import (
	"strings"
	"testing"

	"github.com/konradasb/rungar/internal/version"
)

func TestString(t *testing.T) {
	got := version.String()

	for _, want := range []string{version.Version, version.BuildDate} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, want it to carry %q", got, want)
		}
	}
}
