// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	ghscaleset "github.com/actions/scaleset"

	"github.com/konradasb/rungar/internal/types"
)

// defaultRunnerGroupID is the ID of GitHub's Default runner group.
const defaultRunnerGroupID = 1

// findScaleSet returns GitHub's scale set of this name in the named runner
// group, or nil if there is none, and the group's ID.
func findScaleSet(ctx context.Context, api API, name, group string) (*ghscaleset.RunnerScaleSet, int, error) {
	groupID := defaultRunnerGroupID
	if group != types.DefaultRunnerGroup {
		g, err := api.GetRunnerGroupByName(ctx, group)
		if err != nil {
			return nil, 0, fmt.Errorf("runner group %q: %w", group, err)
		}
		groupID = g.ID
	}

	set, err := api.GetRunnerScaleSet(ctx, groupID, name)
	if err != nil {
		return nil, 0, fmt.Errorf("look up scale set %q: %w", name, err)
	}

	return set, groupID, nil
}

// ensureOnGitHub returns the ID of GitHub's scale set of this name in its
// runner group, creating it if there is none. Using an existing one keeps the
// jobs already assigned to it.
func (s *scaleSet) ensureOnGitHub(ctx context.Context) (int, error) {
	spec, logger := s.spec, s.logger

	existing, groupID, err := findScaleSet(ctx, s.github, spec.Name, spec.RunnerGroup)
	if err != nil {
		return 0, err
	}

	if existing != nil {
		logger.Info("using the scale set GitHub already has", slog.Int("id", existing.ID))

		if have := labelNames(existing.Labels); !sameLabels(have, spec.AllLabels()) {
			logger.Warn("the scale set's labels on GitHub are not the configured ones, and GitHub does not "+
				"change them once a scale set is made; delete it on GitHub for Rungar to make it again with these",
				slog.Any("on_github", have), slog.Any("configured", spec.AllLabels()))
		}

		return existing.ID, nil
	}

	labels := make([]ghscaleset.Label, 0, len(spec.AllLabels()))
	for _, label := range spec.AllLabels() {
		labels = append(labels, ghscaleset.Label{Name: label})
	}

	created, err := s.github.CreateRunnerScaleSet(ctx, &ghscaleset.RunnerScaleSet{
		Name:          spec.Name,
		RunnerGroupID: groupID,
		Labels:        labels,
		RunnerSetting: ghscaleset.RunnerSetting{DisableUpdate: true},
	})
	if err != nil {
		return 0, fmt.Errorf("create scale set %q: %w", spec.Name, err)
	}

	logger.Info("created scale set", slog.Int("id", created.ID), slog.Any("labels", spec.AllLabels()))

	return created.ID, nil
}

// lookUpScaleSet returns what GitHub has of a scale set in the named runner
// group. An error asking is carried in the result.
func (m *Manager) lookUpScaleSet(ctx context.Context, name, group string) *types.GitHubScaleSet {
	set, _, err := findScaleSet(ctx, m.github, name, group)

	switch {
	case err != nil:
		return &types.GitHubScaleSet{Err: err}
	case set == nil:
		return &types.GitHubScaleSet{}
	}

	out := &types.GitHubScaleSet{
		Found:       true,
		ID:          set.ID,
		RunnerGroup: set.RunnerGroupName,
		Labels:      labelNames(set.Labels),
		CreatedAt:   set.CreatedOn,
	}
	if st := set.Statistics; st != nil {
		out.Statistics = &types.ScaleSetStatistics{
			AssignedJobs:      st.TotalAssignedJobs,
			RunningJobs:       st.TotalRunningJobs,
			RegisteredRunners: st.TotalRegisteredRunners,
			BusyRunners:       st.TotalBusyRunners,
			IdleRunners:       st.TotalIdleRunners,
		}
	}

	return out
}

// labelNames returns the labels' names.
func labelNames(labels []ghscaleset.Label) []string {
	names := make([]string, 0, len(labels))
	for _, label := range labels {
		names = append(names, label.Name)
	}

	return names
}

// sameLabels reports whether have and want are the same labels, compared as
// GitHub compares them: in any order, ignoring case.
func sameLabels(have, want []string) bool {
	wanted := make(map[string]bool, len(want))
	for _, label := range want {
		wanted[strings.ToLower(label)] = true
	}

	seen := make(map[string]bool, len(have))
	for _, label := range have {
		name := strings.ToLower(label)
		if !wanted[name] {
			return false
		}

		seen[name] = true
	}

	return len(seen) == len(wanted)
}
