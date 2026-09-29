// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// scaleSet is what the tests read of a scale set in rungar scale-sets inspect
// --json.
type scaleSet struct {
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
	Phase      string `json:"phase"`
	RunsOn     string `json:"runs_on"`
	GitHub     struct {
		Found bool   `json:"found"`
		ID    string `json:"id"`
	} `json:"github"`
}

// inspectScaleSet reads the tests' scale set.
func (e *environment) inspectScaleSet(t *testing.T) scaleSet {
	t.Helper()

	out := e.rungar(t, "scale-sets", "inspect", scaleSetName, "--json")

	return decode[struct {
		ScaleSet scaleSet `json:"scale_set"`
	}](t, out, "the scale set").ScaleSet
}

// waitForListening waits until the scale set is taking jobs.
func (e *environment) waitForListening(t *testing.T) scaleSet {
	t.Helper()

	var set scaleSet
	e.eventually(t, 2*time.Minute, "the scale set to listen", func() string {
		if set = e.inspectScaleSet(t); set.Phase != "SCALE_SET_PHASE_LISTENING" {
			return "phase " + set.Phase
		}

		return ""
	})

	return set
}

// TestTheScaleSetIsOnGitHub checks the daemon found or made its scale set on
// GitHub, and says how a workflow targets it.
func TestTheScaleSetIsOnGitHub(t *testing.T) {
	set := env.waitForListening(t)

	if !set.Configured || !set.GitHub.Found || set.GitHub.ID == "" {
		t.Errorf("scale set = %+v, want it configured and on GitHub", set)
	}
	if set.RunsOn != "runs-on: "+scaleSetName {
		t.Errorf("runs-on = %q, want the line that targets %s", set.RunsOn, scaleSetName)
	}
}

// TestReconcileReportsTheScaleSet checks rungar reconcile reconciles the scale
// set at once and reports it.
func TestReconcileReportsTheScaleSet(t *testing.T) {
	env.waitForReady(t)

	if out := env.rungar(t, "reconcile", scaleSetName); !strings.Contains(out, scaleSetName+":") {
		t.Errorf("rungar reconcile said %q, want the scale set's runners", out)
	}
}
