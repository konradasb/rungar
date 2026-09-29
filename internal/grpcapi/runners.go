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

// DeleteRunner removes a runner: its registration, then its machine.
func (s *Server) DeleteRunner(ctx context.Context, req *rungarv1.DeleteRunnerRequest) (*rungarv1.Runner, error) {
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
		MachineState: string(r.MachineState),
		GithubStatus: gitHubStatusToProto(r.GitHubStatus),
		JobId:        r.JobID,
		CreateTime:   timestamp(r.CreatedAt),
	}
}

// runnerStateToProto converts what a runner is doing.
func runnerStateToProto(s types.RunnerState) rungarv1.RunnerState {
	switch s {
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

// gitHubStatusToProto converts what GitHub says a runner is doing.
func gitHubStatusToProto(r types.GitHubStatus) rungarv1.RunnerGitHubStatus {
	switch r {
	case types.GitHubBusy:
		return rungarv1.RunnerGitHubStatus_RUNNER_GIT_HUB_STATUS_BUSY
	case types.GitHubIdle:
		return rungarv1.RunnerGitHubStatus_RUNNER_GIT_HUB_STATUS_IDLE
	case types.GitHubOffline:
		return rungarv1.RunnerGitHubStatus_RUNNER_GIT_HUB_STATUS_OFFLINE
	case types.GitHubNotRegistered:
		return rungarv1.RunnerGitHubStatus_RUNNER_GIT_HUB_STATUS_NOT_REGISTERED
	default:
		return rungarv1.RunnerGitHubStatus_RUNNER_GIT_HUB_STATUS_UNSPECIFIED
	}
}
