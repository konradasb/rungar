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
func (m *Manager) RemoveScaleSet(ctx context.Context, name, runnerGroup string) (types.ScaleSetRemoval, error) {
	if runnerGroup == "" {
		runnerGroup = types.DefaultRunnerGroup
	}

	if _, ok := m.configuredScaleSet(name); ok {
		return types.ScaleSetRemoval{}, errdefs.InvalidArgument("scale set %q is configured: remove it from "+
			"the configuration first, or the daemon will create it again", name)
	}

	machines, err := m.listAllMachines(ctx, name)
	if err != nil {
		return types.ScaleSetRemoval{}, fmt.Errorf("remove scale set %q: some providers cannot be listed, "+
			"and may have its runners running jobs; try again once they are reachable: %w", name, err)
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

	set, _, err := findScaleSet(ctx, m.github, name, runnerGroup)
	switch {
	case err != nil:
		return out, err
	case set == nil && len(out.Removed) == 0:
		return out, errdefs.NotFound("no scale set %q: it has no runners, and GitHub has none of that name "+
			"in runner group %q", name, runnerGroup)
	case set == nil:
		return out, nil
	}

	if err := m.github.DeleteRunnerScaleSet(ctx, set.ID); err != nil {
		return out, fmt.Errorf("remove scale set %q from GitHub: %w", name, err)
	}

	m.logger.Info("scale set removed from GitHub", slog.String("scale_set", name), slog.Int("scale_set_id", set.ID))
	out.ScaleSetID = set.ID

	return out, nil
}

// RemoveRunner removes a runner, its registration first, and returns it as it
// was. A runner of a configured scale set is removed by that scale set, which
// refuses one it knows is busy or is still being created, and returns
// checkHeld's error unless it holds its session. A runner with no machine has
// only its registration removed.
func (m *Manager) RemoveRunner(ctx context.Context, name string) (types.Runner, error) {
	machines, err := m.listAllMachines(ctx, "")
	if _, err := unreachableOf(err); err != nil {
		return types.Runner{}, err
	}

	i := slices.IndexFunc(machines, func(machine types.Machine) bool { return machine.Name == name })
	if i < 0 {
		return m.removeOrphan(ctx, name)
	}

	machine := machines[i]
	r := runnerOf(machine)

	if s, ok := m.configuredScaleSet(r.ScaleSet); ok {
		return r, s.removeUnlessBusy(ctx, machine)
	}

	return r, m.removeUnconfigured(ctx, machine)
}

// removeUnconfigured removes a runner that no configured scale set has: its
// registration, which GitHub refuses for a runner running a job, then its
// machine. A stopped machine is deleted whatever GitHub says. A runner running
// a job is left, with an errdefs.ErrBusy error.
func (m *Manager) removeUnconfigured(ctx context.Context, machine types.Machine) error {
	if err := removeRegistration(ctx, m.github, machine.Name); err != nil {
		switch {
		case errors.Is(err, errdefs.ErrBusy):
			return errdefs.Busy("runner %q is running a job, so it is left to finish it", machine.Name)
		case machine.State.Alive():
			return fmt.Errorf("runner %q: its registration could not be removed, so it is left: %w", machine.Name, err)
		}
	}

	if err := m.fleet.Delete(ctx, machine.Provider, machine.Name); err != nil {
		return fmt.Errorf("delete machine of runner %q: %w", machine.Name, err)
	}

	scaleSet := machine.Labels[types.LabelScaleSet]
	m.metrics.CountRunnerRemoved(scaleSet, machine.Provider, types.RemovalRequested)
	m.logger.Info("runner removed", slog.String("runner", machine.Name), slog.String("provider", machine.Provider))
	m.events.Record(removedEvent(scaleSet, machine.Name, machine.Provider, types.RemovalRequested))

	return nil
}

// removeUnlessBusy removes on request the runner of a machine listed on the
// fleet, as remove does. It returns checkHeld's error unless the scale set
// holds its session, an errdefs.ErrBusy error for a runner known to be running
// a job, and an errdefs.ErrUnavailable error for one still being created. A
// machine the scale set has yet to adopt is adopted first, as its next
// reconcile pass would. That of a runner that has just left, or could not be
// created, is only deleted: what became of the runner is already accounted
// for.
func (s *scaleSet) removeUnlessBusy(ctx context.Context, machine types.Machine) error {
	name := machine.Name

	// Held throughout, so that the session is not lost and the runners
	// forgotten between the check and the removal, and so that no reconcile
	// pass adopts the machine meanwhile.
	s.passLock.Lock()
	defer s.passLock.Unlock()

	if err := s.checkHeld(); err != nil {
		return err
	}

	s.mu.Lock()
	creating := s.creating[name]
	_, left := s.left[name]
	unknown := !s.tracks(name)
	s.mu.Unlock()

	switch {
	case creating:
		return errdefs.Unavailable("runner %q is still being created; try again once it is", name)
	case left:
		return s.deleteMachine(ctx, machine.Provider, name)
	}

	s.adoptUnknown([]types.Machine{machine})

	s.mu.Lock()
	runner, ok := s.runners[name]
	busy := ok && runner.State == types.RunnerBusy
	s.mu.Unlock()

	if busy {
		return errdefs.Busy("runner %q is running a job", name)
	}

	// A stopped machine the scale set did not know is deleted whatever GitHub
	// says, as one of a scale set not configured is.
	return s.remove(ctx, name, types.RemovalRequested, unknown && !machine.State.Alive())
}

// remove removes a runner and its machine, counting the removal under reason.
// A runner not known to be busy has its registration removed first; GitHub
// refuses that for a runner it has just given a job, which is then marked busy
// and left, with an errdefs.ErrBusy error. Forced, it deletes the machine
// whatever GitHub says, even while the runner runs a job. A runner already
// gone is not an error. A machine that cannot be deleted is returned as an
// error, and deleted by reconciliation. A runner whose machine has ended, and
// whose outcome GitHub has not yet given, takes reason as its outcome.
func (s *scaleSet) remove(ctx context.Context, name string, reason types.RemovalReason, force bool) error {
	s.mu.Lock()
	runner, ok := s.runners[name]
	busy := ok && runner.State == types.RunnerBusy
	if l, leaving := s.leaving[name]; leaving && !l.outcome.known() {
		l.outcome = outcome{removal: reason}
	}
	s.mu.Unlock()

	if !ok {
		return s.settle(ctx, name, s.now())
	}

	switch {
	case force:
		s.tryRemoveRegistration(ctx, name)
	case !busy:
		if err := s.removeRegistrationUnlessBusy(ctx, name); err != nil {
			return err
		}
	}

	if !s.letGo(name, outcome{removal: reason}, time.Time{}) {
		return nil
	}

	return s.settle(ctx, name, s.now())
}

// removeRegistrationUnlessBusy removes a runner's registration. If GitHub
// refuses because the runner has just been given a job, the runner is marked
// busy and an errdefs.ErrBusy error returned.
func (s *scaleSet) removeRegistrationUnlessBusy(ctx context.Context, name string) error {
	err := removeRegistration(ctx, s.github, name)
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
// how many it removed: those created from an older spec first, then the oldest,
// so that the reserve kept is the freshest. It stops once the scale set no
// longer serves.
func (s *scaleSet) removeIdle(ctx context.Context, n int) int {
	var candidates []types.Runner
	for _, r := range s.sortedRunners() {
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
		if removed == n || !s.serving.Load() {
			break
		}

		switch err := s.remove(ctx, r.Name, types.RemovalScaledDown, false); {
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

// tryRemoveRegistration removes a runner's registration, logging a failure.
func (s *scaleSet) tryRemoveRegistration(ctx context.Context, name string) {
	if err := removeRegistration(ctx, s.github, name); err != nil {
		s.logger.Warn("cannot remove the runner's registration from GitHub",
			slog.String("runner", name), slog.Any("error", err))
	}
}

// deleteMachine deletes a runner's machine from a provider.
func (s *scaleSet) deleteMachine(ctx context.Context, provider, name string) error {
	ctx, cancel := context.WithTimeout(ctx, deleteMachineTimeout)
	defer cancel()

	if err := s.providers.Delete(ctx, provider, name); err != nil {
		return fmt.Errorf("delete machine of runner %q: %w", name, err)
	}

	return nil
}

// removeRegistration removes a runner's registration from GitHub, which a
// runner that never ran a job would otherwise leave listed as offline. It
// returns an errdefs.ErrBusy error for a runner running a job, and nil for one
// GitHub no longer has.
func removeRegistration(ctx context.Context, api API, name string) error {
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
