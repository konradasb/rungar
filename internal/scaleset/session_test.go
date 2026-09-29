// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
	"github.com/konradasb/rungar/internal/version"
)

func TestSessionOwnerIsDistinctive(t *testing.T) {
	owner := sessionOwner("rungar-vm")

	if owner == "" {
		t.Fatal("sessionOwner() returned nothing")
	}
	if !strings.HasPrefix(owner, "rungar") {
		t.Errorf("sessionOwner() = %q, want it to name Rungar", owner)
	}
}

func TestSystemInfoNamesRungar(t *testing.T) {
	info := systemInfo(7)

	if info.System != "rungar" || info.ScaleSetID != 7 || info.Version != version.Version {
		t.Errorf("systemInfo(7) = %+v, want Rungar's, of scale set 7", info)
	}
}

// TestASecondSessionIsRecognisedAsAConflict checks GitHub's refusal of a
// second message session is told apart from other failures.
func TestASecondSessionIsRecognisedAsAConflict(t *testing.T) {
	conflict := errors.New(`failed to create message session: request POST https://broker.actions.githubusercontent.com/` +
		`rest/_apis/runtime/runnerscalesets/3/sessions failed(status="409 Conflict"): unexpected status code 409 ` +
		`Conflict: GitHub.Actions.Runtime.WebApi.RunnerScaleSetSessionConflictException, GitHub.Actions.Runtime.WebApi: ` +
		`The actions runner scaleset rungar-c2-m4 already has an active session.`)

	if !isSessionConflict(conflict) {
		t.Error("GitHub's refusal of a second session was not recognised")
	}
	if isSessionConflict(errors.New("401 Unauthorized")) {
		t.Error("another failure was taken for a session conflict")
	}
}

// TestAnUnreachableGitHubIsToldApartFromARefusal checks a request GitHub
// never answered is told apart from one it refused.
func TestAnUnreachableGitHubIsToldApartFromARefusal(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "no connection", err: unansweredError(), want: true},
		{name: "another session", err: errors.New(`request POST https://broker.actions.githubusercontent.com/` +
			`rest/_apis/runtime/runnerscalesets/3/sessions failed(status="409 Conflict"): ` +
			`GitHub.Actions.Runtime.WebApi.RunnerScaleSetSessionConflictException`)},
		{name: "refused", err: errors.New(`request POST https://broker.actions.githubusercontent.com/` +
			`rest/_apis/runtime/runnerscalesets/3/sessions failed(status="401 Unauthorized"): unknown error`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isUnanswered(tt.err); got != tt.want {
				t.Errorf("isUnanswered() = %t, want %t", got, tt.want)
			}
		})
	}
}

// TestSessionIsRetriedWhileGitHubDoesNotAnswer checks a scale set keeps trying
// to open its session while GitHub cannot be reached, rather than failing and
// taking the daemon with it, and says so meanwhile.
func TestSessionIsRetriedWhileGitHubDoesNotAnswer(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
	s := newTestScaleSet(fleet)
	sessions := &fakeSessions{unanswered: 1 << 30}

	serveScaleSet(t, s, sessions)
	waitFor(t, "the scale set to find GitHub unreachable", func() bool {
		return s.currentPhase() == types.ScaleSetGitHubUnreachable
	})

	err := s.reconcile(context.Background())
	if !errors.Is(err, errdefs.ErrUnavailable) || !strings.Contains(err.Error(), "GitHub does not answer") {
		t.Errorf("reconcile() while GitHub does not answer = %v, want an ErrUnavailable saying so", err)
	}

	sessions.setUnanswered(0)
	waitFor(t, "the scale set to listen", func() bool { return s.currentPhase() == types.ScaleSetListening })

	if got := sessions.openedCount(); got != 1 {
		t.Errorf("opened %d sessions, want 1", got)
	}
}

// serveScaleSet serves s over sessions until the test ends.
func serveScaleSet(t *testing.T, s *scaleSet, sessions *fakeSessions) {
	t.Helper()

	s.openMessageSession = sessions.open
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.serve(ctx, 1) }()

	t.Cleanup(func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("serve() = %v, want context.Canceled once cancelled", err)
		}
	})
}

// TestStandbyLeavesTheFleetAlone checks a scale set whose session another
// daemon holds does nothing to the fleet, and refuses to, until it has the
// session.
func TestStandbyLeavesTheFleetAlone(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
	fleet.put("a", "rungar-vm-stopped", types.MachineStopped, time.Now().Add(-time.Hour))
	fleet.put("a", "rungar-vm-running", types.MachineRunning, time.Now().Add(-time.Hour))
	s := newTestScaleSet(fleet, runningAJob)
	sessions := &fakeSessions{other: true}

	serveScaleSet(t, s, sessions)
	waitFor(t, "the scale set to stand by", func() bool {
		return s.currentPhase() == types.ScaleSetWaitingForSession
	})

	err := s.reconcile(context.Background())
	if !errors.Is(err, errdefs.ErrUnavailable) || !strings.Contains(err.Error(), "standby") {
		t.Errorf("reconcile() on standby = %v, want an ErrUnavailable saying so", err)
	}
	if fleet.providerOf("rungar-vm-stopped") == "" {
		t.Error("the standby removed a stopped runner")
	}
	if s.runnerCount() != 0 {
		t.Errorf("the standby has %d runners, want none", s.runnerCount())
	}

	sessions.setOther(false)
	waitFor(t, "the scale set to listen", func() bool { return s.currentPhase() == types.ScaleSetListening })

	if fleet.providerOf("rungar-vm-stopped") != "" {
		t.Error("the stopped runner was not removed once the session was held")
	}
	if s.runnerCount() != 1 {
		t.Errorf("adopted %d runners, want the running one", s.runnerCount())
	}
}

// TestLostSessionIsWaitedForAgain checks a scale set that loses its session
// forgets its runners, leaving them on the fleet, and stands by until it can
// have the session again.
func TestLostSessionIsWaitedForAgain(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
	fleet.put("a", "rungar-vm-running", types.MachineRunning, time.Now().Add(-time.Hour))
	s := newTestScaleSet(fleet, runningAJob)
	sessions := &fakeSessions{}

	serveScaleSet(t, s, sessions)
	waitFor(t, "the scale set to listen", func() bool { return s.currentPhase() == types.ScaleSetListening })

	sessions.setOther(true)
	waitFor(t, "the scale set to stand by", func() bool {
		return s.currentPhase() == types.ScaleSetWaitingForSession
	})

	if s.runnerCount() != 0 {
		t.Errorf("the scale set still has %d runners after losing its session", s.runnerCount())
	}
	if _, known := s.desiredCount(); known {
		t.Error("the scale set kept its last scaling decision after losing its session")
	}
	if err := s.reconcile(context.Background()); !errors.Is(err, errdefs.ErrUnavailable) {
		t.Errorf("reconcile() after losing the session = %v, want an ErrUnavailable", err)
	}
	if fleet.providerOf("rungar-vm-running") == "" {
		t.Error("the runner was removed when the session was lost")
	}

	sessions.setOther(false)
	waitFor(t, "the scale set to listen again", func() bool { return s.currentPhase() == types.ScaleSetListening })

	if got := sessions.openedCount(); got != 2 {
		t.Errorf("opened %d sessions, want 2", got)
	}
	if s.runnerCount() != 1 {
		t.Errorf("adopted %d runners on taking the session back, want 1", s.runnerCount())
	}
}

// TestFollowerStandsByWithTheLead checks a scale set following the lead asks
// for no session until its daemon holds the lead's, and lets its own go, with
// its runners, when the lead loses it.
func TestFollowerStandsByWithTheLead(t *testing.T) {
	leadFleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
	fleet.put("a", "rungar-vm-running", types.MachineRunning, time.Now().Add(-time.Hour))

	l := newLease("lead")
	lead, follower := newTestScaleSet(leadFleet), newTestScaleSet(fleet, runningAJob)
	lead.lease, lead.isLead = l, true
	follower.lease = l
	leadSessions, sessions := &fakeSessions{other: true}, &fakeSessions{}

	serveScaleSet(t, lead, leadSessions)
	serveScaleSet(t, follower, sessions)
	waitFor(t, "the follower to stand by", func() bool {
		return follower.currentPhase() == types.ScaleSetWaitingForLead
	})

	err := follower.reconcile(context.Background())
	if !errors.Is(err, errdefs.ErrUnavailable) || !strings.Contains(err.Error(), `"lead"`) {
		t.Errorf("reconcile() of a follower on standby = %v, want an ErrUnavailable naming the lead", err)
	}
	if got := sessions.openedCount(); got != 0 {
		t.Errorf("the follower opened %d sessions while the lead had none, want none", got)
	}

	leadSessions.setOther(false)
	waitFor(t, "the follower to listen", func() bool { return follower.currentPhase() == types.ScaleSetListening })

	if follower.runnerCount() != 1 {
		t.Errorf("the follower adopted %d runners, want 1", follower.runnerCount())
	}

	leadSessions.setOther(true)
	waitFor(t, "the follower to stand by again", func() bool {
		return follower.currentPhase() == types.ScaleSetWaitingForLead && !sessions.held()
	})

	if follower.runnerCount() != 0 {
		t.Errorf("the follower still has %d runners after the lead lost its session", follower.runnerCount())
	}
	if fleet.providerOf("rungar-vm-running") == "" {
		t.Error("the follower's runner was removed when the lead lost its session")
	}
}
