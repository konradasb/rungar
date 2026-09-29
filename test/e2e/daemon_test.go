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

// TestARestartAdoptsTheRunners checks a restarted daemon adopts the runners
// the last one left, finding them on the fleet, and makes no new ones.
func TestARestartAdoptsTheRunners(t *testing.T) {
	before := env.waitForReady(t)

	env.restartDaemon(t)

	env.eventually(t, 2*time.Minute, "the runners to be adopted", func() string {
		after := env.runners(t)
		for _, r := range before {
			if !slices.ContainsFunc(after, func(a runner) bool { return a.Name == r.Name && a.ready() }) {
				return r.Name + " is not adopted as ready: " + describe(after)
			}
		}
		if len(after) != len(before) {
			return "a runner was made or lost: " + describe(after)
		}

		return ""
	})
}

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

// TestMetricsAreServed checks the Prometheus endpoint reports the scale set's
// runners.
func TestMetricsAreServed(t *testing.T) {
	env.waitForReady(t)

	env.eventually(t, time.Minute, "the scale set's runners in the metrics", func() string {
		ctx, cancel := commandContext(t)
		defer cancel()

		out, err := env.host.run(ctx, "curl", "-sf", "http://"+env.paths.metrics+"/metrics")
		if err != nil {
			return err.Error()
		}
		if !strings.Contains(out, `rungar_runners_current{scale_set="`+scaleSetName+`"`) {
			return "no rungar_runners_current for " + scaleSetName
		}

		return ""
	})
}
