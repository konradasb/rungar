// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"

	"github.com/konradasb/rungar/internal/scaleset"
	"github.com/konradasb/rungar/internal/types"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// ListRunners returns the installation's runners, optionally of one scale set
// or on one provider.
func (s *Server) ListRunners(
	ctx context.Context, req *rungarv1.ListRunnersRequest,
) (*rungarv1.ListRunnersResponse, error) {
	runners, err := s.scaleSets.Runners(ctx, scaleset.RunnerFilter{
		ScaleSet: req.GetScaleSet(),
		Provider: req.GetProvider(),
	})
	if err != nil {
		return nil, err
	}

	return &rungarv1.ListRunnersResponse{
		Runners:              runnersToProto(runners.Items),
		UnreachableProviders: errorsToProto(runners.Unreachable),
		GithubError:          errString(runners.GitHubErr),
	}, nil
}

// RemoveRunner removes a runner: its registration, then its machine.
func (s *Server) RemoveRunner(ctx context.Context, req *rungarv1.RemoveRunnerRequest) (*rungarv1.Runner, error) {
	r, err := s.scaleSets.RemoveRunner(ctx, req.GetName())
	if err != nil {
		return nil, err
	}

	return runnerToProto(r), nil
}

// runnersToProto converts runners.
func runnersToProto(runners []types.Runner) []*rungarv1.Runner {
	out := make([]*rungarv1.Runner, 0, len(runners))
	for _, r := range runners {
		out = append(out, runnerToProto(r))
	}

	return out
}

// runnerToProto converts a runner.
func runnerToProto(r types.Runner) *rungarv1.Runner {
	return &rungarv1.Runner{
		Name:         r.Name,
		ScaleSet:     r.ScaleSet,
		Provider:     r.Provider,
		State:        runnerStateToProto(r.State),
		MachineState: machineStateToProto(r.MachineState),
		GithubStatus: gitHubStatusToProto(r.GitHubStatus),
		CreateTime:   timestamp(r.CreatedAt),
		Job:          jobToProto(r.Job),
	}
}

// jobToProto converts the job a runner is running, or returns nil if there is
// none.
func jobToProto(j types.Job) *rungarv1.RunnerJob {
	if j.ID == "" {
		return nil
	}

	return &rungarv1.RunnerJob{
		Id:            j.ID,
		Repository:    j.Repository,
		WorkflowRef:   j.WorkflowRef,
		DisplayName:   j.DisplayName,
		WorkflowRunId: j.WorkflowRunID,
		StartTime:     timestamp(j.StartedAt),
	}
}

// runnerStateToProto converts what a runner is doing.
func runnerStateToProto(state types.RunnerState) rungarv1.RunnerState {
	switch state {
	case types.RunnerStarting:
		return rungarv1.RunnerState_RUNNER_STATE_STARTING
	case types.RunnerIdle:
		return rungarv1.RunnerState_RUNNER_STATE_IDLE
	case types.RunnerBusy:
		return rungarv1.RunnerState_RUNNER_STATE_BUSY
	default:
		return rungarv1.RunnerState_RUNNER_STATE_UNSPECIFIED
	}
}

// machineStateToProto converts the state of a runner's machine.
func machineStateToProto(state types.MachineState) rungarv1.MachineState {
	switch state {
	case types.MachineStarting:
		return rungarv1.MachineState_MACHINE_STATE_STARTING
	case types.MachineRunning:
		return rungarv1.MachineState_MACHINE_STATE_RUNNING
	case types.MachineStopped:
		return rungarv1.MachineState_MACHINE_STATE_STOPPED
	default:
		return rungarv1.MachineState_MACHINE_STATE_UNSPECIFIED
	}
}

// gitHubStatusToProto converts what GitHub says a runner is doing.
func gitHubStatusToProto(s types.GitHubStatus) rungarv1.RunnerGitHubStatus {
	switch s {
	case types.GitHubBusy:
		return rungarv1.RunnerGitHubStatus_RUNNER_GITHUB_STATUS_BUSY
	case types.GitHubIdle:
		return rungarv1.RunnerGitHubStatus_RUNNER_GITHUB_STATUS_IDLE
	case types.GitHubOffline:
		return rungarv1.RunnerGitHubStatus_RUNNER_GITHUB_STATUS_OFFLINE
	case types.GitHubNotRegistered:
		return rungarv1.RunnerGitHubStatus_RUNNER_GITHUB_STATUS_NOT_REGISTERED
	default:
		return rungarv1.RunnerGitHubStatus_RUNNER_GITHUB_STATUS_UNSPECIFIED
	}
}
