// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// TestStatusReportsScaleSetAndProviders checks rungar status reports the
// daemon, the scale set and the providers.
func TestStatusReportsScaleSetAndProviders(t *testing.T) {
	env.waitForListening(t)

	out := env.rungar(t, "status")

	for _, want := range append([]string{scaleSetName, "LISTENING", "Installation", installation}, env.providers...) {
		if !strings.Contains(out, want) {
			t.Errorf("rungar status does not say %q:\n%s", want, out)
		}
	}
}
