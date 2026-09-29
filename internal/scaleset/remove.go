// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	ghscaleset "github.com/actions/scaleset"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

// deleteMachineTimeout bounds deleting one machine: the listener's contexts are
// never cancelled.
const deleteMachineTimeout = 2 * time.Minute

// RemoveScaleSet removes a scale set no longer configured: its runners, and,
// once none is left running a job, the scale set on GitHub. Called again, it
// carries on where it left off. Nothing is removed while a provider cannot be
// listed, since it may hold the scale set's runners. A scale set with no
// runners that GitHub does not have is an errdefs.ErrNotFound error.
func (m *Manager) RemoveScaleSet(ctx context.Context, name, group string) (types.ScaleSetRemoval, error) {
	if group == "" {
		group = types.DefaultRunnerGroup
	}

	if _, ok := m.configured(name); ok {
		return types.ScaleSetRemoval{}, errdefs.InvalidArgument("scale set %q is configured: remove it from "+
			"the configuration first, or the daemon will make it again", name)
	}

	machines, err := m.listAllMachines(ctx, name)
	if err != nil {
		return types.ScaleSetRemoval{}, fmt.Errorf("not removing scale set %q: its runners on some providers "+
			"cannot be seen, and may be running jobs: %w", name, err)
	}

	var out types.ScaleSetRemoval

	for _, machine := range machines {
		switch err := m.removeUnconfigured(ctx, machine); {
		case errors.Is(err, errdefs.ErrBusy):
			out.BusyLeft++
		case err != nil:
			return out, err
		default:
			out.Removed = append(out.Removed, runnerOf(machine))
		}
	}

	if out.BusyLeft > 0 {
		return out, nil
	}

	set, _, err := findScaleSet(ctx, m.github, name, group)
	switch {
	case err != nil:
		return out, err
	case set == nil && len(out.Removed) == 0:
		return out, errdefs.NotFound("no scale set %q: it has no runners, and GitHub has none of that name "+
			"in runner group %q", name, group)
	case set == nil:
		return out, nil
	}

	if err := m.github.DeleteRunnerScaleSet(ctx, set.ID); err != nil {
		return out, fmt.Errorf("remove scale set %q from GitHub: %w", name, err)
	}

	m.logger.Info("scale set removed from GitHub", slog.String("scale_set", name), slog.Int("id", set.ID))
	out.ScaleSetID = set.ID

	return out, nil
}

// RemoveRunner removes a runner, its registration first, and returns it as it
// was. A runner of a configured scale set is removed by that scale set, which
// refuses one it knows is busy.
func (m *Manager) RemoveRunner(ctx context.Context, name string) (types.Runner, error) {
	machines, err := m.listAllMachines(ctx, "")
	unreachable, err := unreachableOf(err)
	if err != nil {
		return types.Runner{}, err
	}

	i := slices.IndexFunc(machines, func(machine types.Machine) bool { return machine.Name == name })
	if i < 0 {
		if len(unreachable) > 0 {
			return types.Runner{}, errdefs.NotFound("no runner %q on the fleet, or on a provider that "+
				"could not be listed", name)
		}

		return types.Runner{}, errdefs.NotFound("no runner %q on the fleet", name)
	}

	machine := machines[i]
	r := runnerOf(machine)

	if s, ok := m.configured(r.ScaleSet); ok && s.adopted.Load() {
		if err := s.removeUnlessBusy(ctx, name); !errors.Is(err, errdefs.ErrNotFound) {
			return r, err
		}
	}

	return r, m.removeUnconfigured(ctx, machine)
}

// removeUnconfigured removes a runner no configured scale set has: its
// registration, which GitHub refuses for a runner running a job, then its
// machine. A stopped machine is removed whatever GitHub says.
func (m *Manager) removeUnconfigured(ctx context.Context, machine types.Machine) error {
	if err := deregister(ctx, m.github, machine.Name); err != nil {
		switch {
		case errors.Is(err, errdefs.ErrBusy):
			return errdefs.Busy("runner %q is running a job, so it is left to finish it", machine.Name)
		case machine.State.Alive():
			return fmt.Errorf("runner %q: its registration could not be removed, so it is left: %w", machine.Name, err)
		}
	}

	if err := m.fleet.Delete(ctx, machine.Provider, machine.Name); err != nil {
		return err
	}

	scaleSet := machine.Labels[types.LabelScaleSet]
	m.metrics.CountRunnerRemoved(scaleSet, machine.Provider, types.RemovalRequested)
	m.logger.Info("runner removed", slog.String("runner", machine.Name), slog.String("provider", machine.Provider))
	m.events.Record(removedEvent(scaleSet, machine.Name, machine.Provider, types.RemovalRequested))

	return nil
}

// removeRunner removes a runner and its machine, counting the removal under
// reason. A runner not known to be busy has its registration removed first;
// GitHub refuses that for a runner it has just given a job, which is then
// marked busy and left, with an errdefs.ErrBusy error. A runner already gone
// is not an error.
func (s *scaleSet) removeRunner(ctx context.Context, name string, reason types.RemovalReason) error {
	return s.remove(ctx, name, reason, false)
}

// removeUnlessBusy removes a runner on request, as removeRunner does. It
// returns an errdefs.ErrBusy error for a runner known to be running a job, and
// an errdefs.ErrNotFound error for one the scale set does not have.
func (s *scaleSet) removeUnlessBusy(ctx context.Context, name string) error {
	s.mu.Lock()
	runner, ok := s.runners[name]
	busy := ok && runner.State == types.RunnerBusy
	s.mu.Unlock()

	switch {
	case !ok:
		return errdefs.NotFound("scale set %q has no runner %q", s.spec.Name, name)
	case busy:
		return errdefs.Busy("runner %q is running a job", name)
	}

	return s.remove(ctx, name, types.RemovalRequested, false)
}

// remove removes a runner and its machine, counting the removal under reason.
// Forced, it removes the machine whatever GitHub says, even while the runner
// runs a job; otherwise it behaves as removeRunner.
func (s *scaleSet) remove(ctx context.Context, name string, reason types.RemovalReason, force bool) error {
	s.mu.Lock()
	runner, ok := s.runners[name]
	s.mu.Unlock()

	if !ok {
		s.logger.Debug("runner already gone", slog.String("runner", name))
		return nil
	}

	switch {
	case force:
		s.tryDeregister(ctx, name)
	case runner.State != types.RunnerBusy:
		if err := s.deregisterUnlessBusy(ctx, name); err != nil {
			return err
		}
	}

	s.mu.Lock()
	_, still := s.runners[name]
	delete(s.runners, name)
	delete(s.offline, name)
	if still {
		s.removed[name] = time.Now()
	}
	s.mu.Unlock()

	if !still {
		return nil
	}

	if err := s.deleteMachine(ctx, runner.Provider, name); err != nil {
		return err
	}

	s.metrics.CountRunnerRemoved(s.spec.Name, runner.Provider, reason)
	s.logger.Info("runner removed", slog.String("runner", name), slog.String("provider", runner.Provider),
		slog.String("reason", string(reason)))
	s.events.Record(removedEvent(s.spec.Name, name, runner.Provider, reason))

	return nil
}

// deregisterUnlessBusy removes a runner's registration. If GitHub refuses
// because the runner has just been given a job, the runner is marked busy and
// an errdefs.ErrBusy error returned.
func (s *scaleSet) deregisterUnlessBusy(ctx context.Context, name string) error {
	err := deregister(ctx, s.github, name)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errdefs.ErrBusy):
		s.mu.Lock()
		if r, ok := s.runners[name]; ok {
			r.State = types.RunnerBusy
		}
		s.mu.Unlock()

		return errdefs.Busy("runner %q is running a job", name)
	default:
		// It might be running a job, so it is left for the next reconcile.
		return fmt.Errorf("remove the registration of runner %q: %w", name, err)
	}
}

// removeIdle removes up to n runners not known to be running a job and returns
// how many it removed: those made from an older spec first, then the oldest,
// so that the reserve kept is the freshest.
func (s *scaleSet) removeIdle(ctx context.Context, n int) int {
	var candidates []types.Runner
	for _, r := range s.list() {
		if r.State != types.RunnerBusy {
			candidates = append(candidates, r)
		}
	}

	outdated := func(r types.Runner) bool { return r.Revision != s.spec.RunnerRevisions[r.Provider] }
	slices.SortStableFunc(candidates, func(a, b types.Runner) int {
		if outdated(a) != outdated(b) {
			if outdated(a) {
				return -1
			}

			return 1
		}

		return a.CreatedAt.Compare(b.CreatedAt)
	})

	var removed int
	for _, r := range candidates {
		if removed == n {
			break
		}

		switch err := s.removeRunner(ctx, r.Name, types.RemovalScaledDown); {
		case err == nil:
			removed++
		case errors.Is(err, errdefs.ErrBusy):
			s.logger.Debug("not removing runner GitHub has just given a job", slog.String("runner", r.Name))
		default:
			s.logger.Warn("cannot remove idle runner", slog.String("runner", r.Name), slog.Any("error", err))
		}
	}

	return removed
}

// forget drops a runner from what the scale set knows, leaving the fleet alone.
func (s *scaleSet) forget(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.offline, name)
	delete(s.runners, name)
}

// tryDeregister removes a runner's registration, logging a failure.
func (s *scaleSet) tryDeregister(ctx context.Context, name string) {
	if err := deregister(ctx, s.github, name); err != nil {
		s.logger.Warn("cannot remove the runner's registration from GitHub",
			slog.String("runner", name), slog.Any("error", err))
	}
}

// deleteMachine removes a machine from a provider.
func (s *scaleSet) deleteMachine(ctx context.Context, on, name string) error {
	ctx, cancel := context.WithTimeout(ctx, deleteMachineTimeout)
	defer cancel()

	if err := s.fleet.Delete(ctx, on, name); err != nil {
		return fmt.Errorf("delete runner %q: %w", name, err)
	}

	return nil
}

// deregister removes a runner's registration from GitHub, which a runner that
// never ran a job would otherwise leave listed as offline. It returns an
// errdefs.ErrBusy error for a runner running a job, and nil for one GitHub no
// longer has.
func deregister(ctx context.Context, api API, name string) error {
	ref, err := api.GetRunnerByName(ctx, name)
	if err != nil {
		return err
	}
	if ref == nil {
		return nil
	}

	err = api.RemoveRunner(ctx, int64(ref.ID))
	if errors.Is(err, ghscaleset.JobStillRunningError) {
		return errdefs.Busy("runner %q is running a job", name)
	}

	return err
}
