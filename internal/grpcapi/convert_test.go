// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"errors"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/types"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// TestEnumsToProto checks every state has its own value, and anything unknown
// is unspecified rather than taken for one.
func TestEnumsToProto(t *testing.T) {
	phases := map[types.ScaleSetPhase]rungarv1.ScaleSetPhase{
		types.ScaleSetStarting:          rungarv1.ScaleSetPhase_SCALE_SET_PHASE_STARTING,
		types.ScaleSetWaitingForSession: rungarv1.ScaleSetPhase_SCALE_SET_PHASE_WAITING_FOR_SESSION,
		types.ScaleSetListening:         rungarv1.ScaleSetPhase_SCALE_SET_PHASE_LISTENING,
		"":                              rungarv1.ScaleSetPhase_SCALE_SET_PHASE_UNSPECIFIED,
	}
	for in, want := range phases {
		if got := scaleSetPhaseToProto(in); got != want {
			t.Errorf("scaleSetPhaseToProto(%q) = %v, want %v", in, got, want)
		}
	}

	states := map[types.RunnerState]rungarv1.RunnerState{
		types.RunnerStarting: rungarv1.RunnerState_RUNNER_STATE_STARTING,
		types.RunnerIdle:     rungarv1.RunnerState_RUNNER_STATE_IDLE,
		types.RunnerBusy:     rungarv1.RunnerState_RUNNER_STATE_BUSY,
		"":                   rungarv1.RunnerState_RUNNER_STATE_UNSPECIFIED,
	}
	for in, want := range states {
		if got := runnerStateToProto(in); got != want {
			t.Errorf("runnerStateToProto(%q) = %v, want %v", in, got, want)
		}
	}

	registrations := map[types.GitHubStatus]rungarv1.RunnerGitHubStatus{
		types.GitHubBusy:          rungarv1.RunnerGitHubStatus_RUNNER_GIT_HUB_STATUS_BUSY,
		types.GitHubIdle:          rungarv1.RunnerGitHubStatus_RUNNER_GIT_HUB_STATUS_IDLE,
		types.GitHubOffline:       rungarv1.RunnerGitHubStatus_RUNNER_GIT_HUB_STATUS_OFFLINE,
		types.GitHubNotRegistered: rungarv1.RunnerGitHubStatus_RUNNER_GIT_HUB_STATUS_NOT_REGISTERED,
		"":                        rungarv1.RunnerGitHubStatus_RUNNER_GIT_HUB_STATUS_UNSPECIFIED,
	}
	for in, want := range registrations {
		if got := gitHubStatusToProto(in); got != want {
			t.Errorf("gitHubStatusToProto(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestErrorsToProto(t *testing.T) {
	if errorsToProto(nil) != nil {
		t.Error("no errors is not nil")
	}

	out := errorsToProto(map[string]error{"compute1": errors.New("refused"), "cloud": nil})
	if out["compute1"] != "refused" || out["cloud"] != "unknown error" {
		t.Errorf("errorsToProto() = %v, want each error, and a nil one said to be unknown", out)
	}

	if errString(nil) != "" || errString(errors.New("x")) != "x" {
		t.Error("errString is not the error's text, or empty")
	}
}

func TestTimestamp(t *testing.T) {
	if timestamp(time.Time{}) != nil {
		t.Error("a time not known is not nil")
	}

	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if !timestamp(at).AsTime().Equal(at) {
		t.Errorf("timestamp(%v) = %v", at, timestamp(at).AsTime())
	}
}
