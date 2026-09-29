// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

//go:build e2e

package e2e

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// bootTimeout bounds a runner's boot: its machine created and started, and
// the Actions runner on it connected to GitHub.
const bootTimeout = 10 * time.Minute

// runner is what the tests read of a runner in rungar runners ls --json.
type runner struct {
	Name         string `json:"name"`
	ScaleSet     string `json:"scale_set"`
	Provider     string `json:"provider"`
	State        string `json:"state"`
	MachineState string `json:"machine_state"`
	GitHubStatus string `json:"github_status"`
}

// ready reports whether a runner can take a job: idle, its machine running,
// and connected to GitHub.
func (r runner) ready() bool {
	return r.State == "RUNNER_STATE_IDLE" && r.MachineState == "MACHINE_STATE_RUNNING" && r.GitHubStatus == "RUNNER_GITHUB_STATUS_IDLE"
}

// runners lists the tests' scale set's runners.
func (e *environment) runners(t *testing.T) []runner {
	t.Helper()

	out := e.rungar(t, "runners", "ls", "--scale-set", scaleSetName, "--json")

	return decode[struct {
		Runners []runner `json:"runners"`
	}](t, out, "runners").Runners
}

// waitForReady waits until the scale set has as many ready runners as it
// keeps, and returns them.
func (e *environment) waitForReady(t *testing.T) []runner {
	t.Helper()

	var ready []runner
	e.eventually(t, bootTimeout, "the scale set's runners to be ready", func() string {
		ready = ready[:0]

		all := e.runners(t)
		for _, r := range all {
			if r.ready() {
				ready = append(ready, r)
			}
		}

		if len(ready) < minRunners {
			return describeRunners(all)
		}

		return ""
	})

	return ready
}

// describeRunners says what a list of runners is doing, for a failure message.
func describeRunners(runners []runner) string {
	if len(runners) == 0 {
		return "no runners"
	}

	parts := make([]string, 0, len(runners))
	for _, r := range runners {
		parts = append(parts, r.Name+" "+r.State+"/"+r.MachineState+"/"+r.GitHubStatus)
	}

	return strings.Join(parts, ", ")
}

// TestMinRunnersAreKeptReady checks the whole path from the daemon to GitHub:
// the scale set's reserve runners are placed, registered and booted, and
// GitHub lists them as idle.
func TestMinRunnersAreKeptReady(t *testing.T) {
	ready := env.waitForReady(t)

	for _, r := range ready {
		if r.ScaleSet != scaleSetName || !slices.Contains(env.providers, r.Provider) {
			t.Errorf("runner %+v, want it of %s on one of %v", r, scaleSetName, env.providers)
		}
	}
}

// TestARemovedRunnerIsReplaced checks rungar runners rm removes a runner and
// the scale set replaces it.
func TestARemovedRunnerIsReplaced(t *testing.T) {
	removed := env.waitForReady(t)[0]

	if out := env.rungar(t, "runners", "rm", removed.Name); !strings.Contains(out, "removed from "+removed.Provider) {
		t.Errorf("rungar runners rm said %q, want it removed from %s", out, removed.Provider)
	}

	env.eventually(t, bootTimeout, "a runner to replace the one removed", func() string {
		runners := env.runners(t)
		if slices.ContainsFunc(runners, func(r runner) bool { return r.Name == removed.Name }) {
			return removed.Name + " is still listed"
		}
		if !slices.ContainsFunc(runners, runner.ready) {
			return describeRunners(runners)
		}

		return ""
	})
}
