// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// TestStatusSaysWhatTheDaemonKnows checks the report shows what only the daemon
// knows: providers held, found full or refusing to create a scale set's runners,
// a scale set holding back, and a busy runner.
func TestStatusSaysWhatTheDaemonKnows(t *testing.T) {
	h := newHarness(t)

	if err := h.run("status"); err != nil {
		t.Fatalf("status() = %v", err)
	}

	h.says(t,
		"https://github.com/my-org", "(organisation)", h.daemon.status.GetDaemon().GetGithub().GetCredentials()+" OK",
		"/etc/rungar/config.yaml",
		"Scale sets (1)", "HOLDING BACK", `scale set "rungar-big" is waiting for room`, "1 (1 busy)", "2 vCPU, 4 GiB",
		"Providers (4)", "RUNNERS", "2/8",
		"UNREACHABLE", "compute2 cannot be reached: connection refused",
		"HELD", "for rungar-big (priority 10), 25s left",
		"rungar-big found cloud full; tries it again in 9s",
		"rungar-gpu found compute3 failing (2 in a row); tries it again in 30s: connection reset",
		"Runners (2)", "rungar-vm-1", "Busy", "Offline", "5m",
		"1 runner; the scale sets allow 4 between them.",
	)
}

// TestStatusClaimsNoSpareCapacity checks the report never says how much room a
// provider has left: Rungar does not ask, it tries a provider and learns it is
// full from the refusal.
func TestStatusClaimsNoSpareCapacity(t *testing.T) {
	h := newHarness(t)

	if err := h.run("status"); err != nil {
		t.Fatalf("status() = %v", err)
	}

	for _, word := range []string{"AVAILABLE", "SPARE", "ROOM", "Room for", "room for", "largest size"} {
		if strings.Contains(h.out.String(), word) {
			t.Errorf("the report says %q:\n%s", word, h.out)
		}
	}
}

// TestStatusExitsZeroWhateverItReports checks a full fleet and rejected
// credentials are reported, not returned as an error.
func TestStatusExitsZeroWhateverItReports(t *testing.T) {
	h := newHarness(t)

	h.daemon.status.Providers = []*rungarv1.Provider{{
		Name: "compute1", Type: "dicer", Reachable: true,
		ScaleSets: []*rungarv1.ProviderScaleSet{{ScaleSet: "rungar-vm", BackoffFor: durationpb.New(15 * time.Second), Full: true}},
	}}
	h.daemon.status.Daemon.Github.Error = "401 Unauthorized: Bad credentials"

	if err := h.run("status"); err != nil {
		t.Fatalf("status() = %v, want the report alone", err)
	}

	h.says(t, h.daemon.status.GetDaemon().GetGithub().GetCredentials(), "401 Unauthorized: Bad credentials",
		"rungar-vm found compute1 full; tries it again in 15s")
}

// TestStatusSaysAScaleSetIsPaused checks status shows a paused scale set,
// what that means for its jobs and runners, and how long it lasts.
func TestStatusSaysAScaleSetIsPaused(t *testing.T) {
	h := newHarness(t)
	h.daemon.status.GetScaleSets()[0].Paused = true

	if err := h.run("status"); err != nil {
		t.Fatal(err)
	}

	h.says(t, scaleSetPaused, "rungar-vm: taking no jobs, which wait on GitHub; 1 runner left; "+
		"until resumed or the daemon restarts")
}

// TestResumedScaleSetConfiguredPausedSaysSo checks status says a scale set
// resumed against its configuration is so only until the daemon restarts.
func TestResumedScaleSetConfiguredPausedSaysSo(t *testing.T) {
	h := newHarness(t)
	set := h.daemon.status.GetScaleSets()[0]
	set.ConfiguredPaused, set.HoldingBackReason = true, ""

	if err := h.run("status"); err != nil {
		t.Fatal(err)
	}

	h.says(t, scaleSetListening, "resumed until the daemon restarts; the configuration has it paused")
}
