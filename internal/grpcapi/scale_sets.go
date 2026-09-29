// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"

	"github.com/konradasb/rungar/internal/types"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// ListScaleSets returns the configured scale sets, then the leftover ones
// found by their runners.
func (s *Server) ListScaleSets(
	ctx context.Context, _ *rungarv1.ListScaleSetsRequest,
) (*rungarv1.ListScaleSetsResponse, error) {
	sets, err := s.scaleSets.ScaleSets(ctx)
	if err != nil {
		return nil, err
	}

	out := &rungarv1.ListScaleSetsResponse{UnreachableProviders: errorsToProto(sets.Unreachable)}
	for _, set := range sets.Items {
		out.ScaleSets = append(out.ScaleSets, scaleSetToProto(set))
	}

	return out, nil
}

// GetScaleSet returns one scale set, configured or not.
func (s *Server) GetScaleSet(ctx context.Context, req *rungarv1.GetScaleSetRequest) (*rungarv1.ScaleSet, error) {
	set, err := s.scaleSets.ScaleSet(ctx, req.GetName(), req.GetRunnerGroup())
	if err != nil {
		return nil, err
	}

	return scaleSetToProto(set), nil
}

// RemoveScaleSet removes a scale set no longer configured: its runners not
// running a job, and, once none is left running one, the scale set on GitHub.
func (s *Server) RemoveScaleSet(
	ctx context.Context, req *rungarv1.RemoveScaleSetRequest,
) (*rungarv1.RemoveScaleSetResponse, error) {
	removal, err := s.scaleSets.RemoveScaleSet(ctx, req.GetName(), req.GetRunnerGroup())
	if err != nil {
		return nil, err
	}

	return &rungarv1.RemoveScaleSetResponse{
		Removed:    runnersToProto(removal.Removed),
		BusyLeft:   int32(removal.BusyLeft),
		ScaleSetId: int64(removal.ScaleSetID),
	}, nil
}

// Reconcile reconciles the named scale sets now, or all of them.
func (s *Server) Reconcile(ctx context.Context, req *rungarv1.ReconcileRequest) (*rungarv1.ReconcileResponse, error) {
	sets, err := s.scaleSets.Reconcile(ctx, req.GetScaleSets())
	if err != nil {
		return nil, err
	}

	out := &rungarv1.ReconcileResponse{}
	for _, set := range sets {
		out.ScaleSets = append(out.ScaleSets, scaleSetToProto(set))
	}

	return out, nil
}

// PauseScaleSet stops a configured scale set taking jobs until it is resumed or
// the daemon restarts.
func (s *Server) PauseScaleSet(_ context.Context, req *rungarv1.PauseScaleSetRequest) (*rungarv1.ScaleSet, error) {
	return s.setPaused(req.GetName(), true)
}

// ResumeScaleSet has a paused scale set take jobs again.
func (s *Server) ResumeScaleSet(_ context.Context, req *rungarv1.ResumeScaleSetRequest) (*rungarv1.ScaleSet, error) {
	return s.setPaused(req.GetName(), false)
}

// setPaused pauses or resumes a scale set, and returns it.
func (s *Server) setPaused(name string, paused bool) (*rungarv1.ScaleSet, error) {
	set, err := s.scaleSets.SetPaused(name, paused)
	if err != nil {
		return nil, err
	}

	return scaleSetToProto(set), nil
}

// scheduleToProto converts a schedule, marking the window at index window
// as in force if inWindow. It returns nil for a schedule without windows.
func scheduleToProto(s types.Schedule, window int, inWindow bool) *rungarv1.Schedule {
	if len(s.Windows) == 0 {
		return nil
	}

	out := &rungarv1.Schedule{TimeZone: s.TimeZone.String()}
	for i, w := range s.Windows {
		out.Windows = append(out.Windows, &rungarv1.ScheduleWindow{
			Days:       w.Days.String(),
			From:       w.From.String(),
			To:         w.To.String(),
			MinRunners: int32(w.MinRunners),
			InForce:    inWindow && i == window,
		})
	}

	return out
}

// scaleSetToProto converts a scale set. Only a configured one has a spec.
func scaleSetToProto(s types.ScaleSet) *rungarv1.ScaleSet {
	status := s.Status
	out := &rungarv1.ScaleSet{
		Name:              s.Spec.Name,
		Configured:        status.Configured,
		Phase:             scaleSetPhaseToProto(status.Phase),
		HoldingBackReason: status.HoldingBackReason,
		FleetRunners:      int32(status.FleetRunners),
		Github:            gitHubScaleSetToProto(status.GitHub),
	}

	if status.Configured {
		spec := s.Spec

		out.Labels = spec.AllLabels()
		out.Providers = spec.ProviderNames()
		out.Placement = string(spec.Placement)
		out.MinRunners = int32(status.MinRunners)
		out.ConfiguredMinRunners = int32(spec.MinRunners)
		out.Schedule = scheduleToProto(spec.Schedule, status.Window, status.InWindow)
		out.MaxRunners = int32(spec.MaxRunners)
		out.Priority = int32(spec.Priority)
		out.RunnerGroup = spec.RunnerGroup
		out.RunsOn = spec.RunsOn()
		out.Paused = status.Paused
		out.ConfiguredPaused = spec.Paused

		for _, name := range spec.ProviderNames() {
			size := &rungarv1.RunnerSize{Provider: name}
			if r := spec.RunnerSpecs[name]; r != nil {
				size.Description = r.Describe()
			}
			out.RunnerSizes = append(out.RunnerSizes, size)
		}
	}

	if status.DesiredKnown {
		out.DesiredRunners = int32(status.Desired)
	}

	if status.Runners != nil {
		out.RunnerCounts = runnerCountsToProto(status.Runners)
	}

	return out
}

// runnerCountsToProto counts runners by state.
func runnerCountsToProto(runners []types.Runner) *rungarv1.RunnerCounts {
	counts := &rungarv1.RunnerCounts{}
	for _, r := range runners {
		switch r.State {
		case types.RunnerStarting:
			counts.Starting++
		case types.RunnerIdle:
			counts.Idle++
		case types.RunnerBusy:
			counts.Busy++
		}
	}

	return counts
}

// gitHubScaleSetToProto converts what GitHub has of a scale set, or nil if it
// was not asked.
func gitHubScaleSetToProto(g *types.GitHubScaleSet) *rungarv1.GitHubScaleSet {
	switch {
	case g == nil:
		return nil
	case g.Err != nil:
		return &rungarv1.GitHubScaleSet{Error: g.Err.Error()}
	case !g.Found:
		return &rungarv1.GitHubScaleSet{}
	}

	out := &rungarv1.GitHubScaleSet{
		Found:       true,
		Id:          int64(g.ID),
		RunnerGroup: g.RunnerGroup,
		Labels:      g.Labels,
		CreateTime:  timestamp(g.CreatedAt),
	}

	if stats := g.Statistics; stats != nil {
		out.Statistics = &rungarv1.ScaleSetStatistics{
			AssignedJobs:      int32(stats.AssignedJobs),
			RunningJobs:       int32(stats.RunningJobs),
			RegisteredRunners: int32(stats.RegisteredRunners),
			BusyRunners:       int32(stats.BusyRunners),
			IdleRunners:       int32(stats.IdleRunners),
		}
	}

	return out
}

// scaleSetPhaseToProto converts a scale set's phase.
func scaleSetPhaseToProto(p types.ScaleSetPhase) rungarv1.ScaleSetPhase {
	switch p {
	case types.ScaleSetStarting:
		return rungarv1.ScaleSetPhase_SCALE_SET_PHASE_STARTING
	case types.ScaleSetWaitingForSession:
		return rungarv1.ScaleSetPhase_SCALE_SET_PHASE_WAITING_FOR_SESSION
	case types.ScaleSetListening:
		return rungarv1.ScaleSetPhase_SCALE_SET_PHASE_LISTENING
	case types.ScaleSetGitHubUnreachable:
		return rungarv1.ScaleSetPhase_SCALE_SET_PHASE_GITHUB_UNREACHABLE
	case types.ScaleSetWaitingForLead:
		return rungarv1.ScaleSetPhase_SCALE_SET_PHASE_WAITING_FOR_LEAD
	default:
		return rungarv1.ScaleSetPhase_SCALE_SET_PHASE_UNSPECIFIED
	}
}
