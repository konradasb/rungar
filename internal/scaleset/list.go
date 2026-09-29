// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

// Configured returns the configured scale sets as they run, without what
// GitHub or the providers have of them.
func (m *Manager) Configured() []types.ScaleSet {
	out := make([]types.ScaleSet, 0, len(m.sets))
	for _, s := range m.sets {
		out = append(out, s.status())
	}

	return out
}

// ScaleSets returns the configured scale sets, then, by name, those found only
// by their runners on the fleet, each with what GitHub has of it. GitHub
// cannot list scale sets, so one neither configured nor with runners is
// missing.
func (m *Manager) ScaleSets(ctx context.Context) (types.ScaleSetList, error) {
	machines, err := m.listAllMachines(ctx, "")
	unreachable, err := unreachableOf(err)
	if err != nil {
		return types.ScaleSetList{}, err
	}

	counts := map[string]int{}
	for _, machine := range machines {
		counts[machine.Labels[types.LabelScaleSet]]++
	}

	out := types.ScaleSetList{
		Items:       make([]types.ScaleSet, 0, len(m.sets)+len(counts)),
		Unreachable: unreachable,
	}

	for _, s := range m.sets {
		set := s.status()
		set.Status.FleetRunners = counts[s.spec.Name]
		set.Status.GitHub = m.lookUpScaleSet(ctx, s.spec.Name, s.spec.RunnerGroup)
		out.Items = append(out.Items, set)
		delete(counts, s.spec.Name)
	}

	for _, name := range slices.Sorted(maps.Keys(counts)) {
		out.Items = append(out.Items, types.ScaleSet{
			Spec: types.ScaleSetSpec{Name: name},
			Status: types.ScaleSetStatus{
				FleetRunners: counts[name],
				GitHub:       m.lookUpScaleSet(ctx, name, types.DefaultRunnerGroup),
			},
		})
	}

	return out, nil
}

// ScaleSet returns one scale set, configured or not, with what GitHub has of
// it in the named runner group; an empty group is the scale set's own, or
// Default.
func (m *Manager) ScaleSet(ctx context.Context, name, group string) (types.ScaleSet, error) {
	set := types.ScaleSet{Spec: types.ScaleSetSpec{Name: name}}
	if s, ok := m.configured(name); ok {
		set = s.status()
		if group == "" {
			group = s.spec.RunnerGroup
		}
	}
	if group == "" {
		group = types.DefaultRunnerGroup
	}

	set.Status.GitHub = m.lookUpScaleSet(ctx, name, group)

	machines, err := m.listAllMachines(ctx, name)
	if _, err := unreachableOf(err); err != nil {
		return types.ScaleSet{}, err
	}
	set.Status.FleetRunners = len(machines)

	if st := set.Status; !st.Configured && !st.GitHub.Found && st.FleetRunners == 0 {
		if err := st.GitHub.Err; err != nil {
			return types.ScaleSet{}, fmt.Errorf("scale set %q: %w", name, err)
		}

		return types.ScaleSet{}, errdefs.NotFound("no scale set %q: it is not configured, has no runners, "+
			"and GitHub has none of that name in runner group %q", name, group)
	}

	return set, nil
}

// RunnerFilter narrows Runners to a scale set, a provider, or both. An empty
// field matches all.
type RunnerFilter struct {
	ScaleSet string
	Provider string
}

// Runners returns the installation's runners on the fleet that the filter
// matches, with what the daemon knows of each and what GitHub says. It lists
// the providers rather than asking the scale sets, so that runners of scale
// sets no longer configured are included.
func (m *Manager) Runners(ctx context.Context, filter RunnerFilter) (types.RunnerList, error) {
	names, err := m.fleet.Names(filter.Provider)
	if err != nil {
		return types.RunnerList{}, err
	}

	machines, err := m.listMachines(ctx, filter.ScaleSet, names)
	unreachable, err := unreachableOf(err)
	if err != nil {
		return types.RunnerList{}, err
	}

	return m.runnersFrom(ctx, machines, unreachable), nil
}

// runnersFrom returns the runners of a fleet listing, with what the daemon
// knows of each and what GitHub says.
func (m *Manager) runnersFrom(ctx context.Context, machines []types.Machine, unreachable map[string]error,
) types.RunnerList {
	out := types.RunnerList{Unreachable: unreachable, Items: make([]types.Runner, 0, len(machines))}
	for _, machine := range machines {
		out.Items = append(out.Items, runnerOf(machine))
	}

	// Sorted, since providers answer in any order.
	slices.SortFunc(out.Items, func(a, b types.Runner) int {
		if c := strings.Compare(a.ScaleSet, b.ScaleSet); c != 0 {
			return c
		}

		return strings.Compare(a.Name, b.Name)
	})

	out.GitHubErr = m.fillGitHubStatuses(ctx, out.Items)

	// Read after asking GitHub, which brings the scale sets' runners in line.
	known := map[string]types.Runner{}
	for _, s := range m.sets {
		for _, r := range s.list() {
			known[r.Name] = r
		}
	}
	for i := range out.Items {
		if k, ok := known[out.Items[i].Name]; ok {
			out.Items[i].State, out.Items[i].JobID = k.State, k.JobID
		}
	}

	return out
}

// runnerOf returns the runner a machine describes.
func runnerOf(m types.Machine) types.Runner {
	return types.Runner{
		Name:         m.Name,
		ScaleSet:     m.Labels[types.LabelScaleSet],
		Provider:     m.Provider,
		MachineState: m.State,
		Size:         m.Size,
		CreatedAt:    m.CreatedAt,
	}
}

// fillGitHubStatuses sets what GitHub says of each runner, from one cached
// listing. A configured scale set's runner is asked through its scale set.
func (m *Manager) fillGitHubStatuses(ctx context.Context, runners []types.Runner) error {
	// Asked even with no runners, so that the answer says whether GitHub
	// takes the daemon's credentials.
	if err := m.gitHubStatuses.RefreshIfStale(ctx); err != nil {
		return err
	}

	for i := range runners {
		ask := m.gitHubStatuses.GitHubStatus
		if s, ok := m.configured(runners[i].ScaleSet); ok {
			ask = s.syncFromGitHub
		}

		r, err := ask(ctx, runners[i].Name)
		if err != nil {
			return err
		}
		runners[i].GitHubStatus = r
	}

	return nil
}
