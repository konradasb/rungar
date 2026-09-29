// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// TestReconcileReportsTheScaleSet checks rungar reconcile reconciles the scale
// set at once and reports it.
func TestReconcileReportsTheScaleSet(t *testing.T) {
	env.waitForReady(t)

	if out := env.rungar(t, "reconcile", scaleSetName); !strings.Contains(out, scaleSetName+":") {
		t.Errorf("rungar reconcile said %q, want the scale set's runners", out)
	}
}
