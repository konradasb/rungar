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

// removeOrphan removes the registration of a runner with no machine on
// the fleet. It refuses one GitHub has connected, whose machine is somewhere
// the fleet cannot see, one of a scale set not configured, and one of a scale
// set not holding its session.
func (m *Manager) removeOrphan(ctx context.Context, name string) (types.Runner, error) {
	status, err := m.gitHubStatuses.GitHubStatus(ctx, name)
	switch {
	case err != nil:
		return types.Runner{}, fmt.Errorf("no runner %q on the fleet, and GitHub cannot be asked about it: %w",
			name, err)
	case status == types.GitHubNotRegistered:
		return types.Runner{}, errdefs.NotFound("no runner %q on the fleet or on GitHub", name)
	case status == types.GitHubBusy:
		return types.Runner{}, errdefs.Busy("runner %q has no machine on the fleet, but is running a job", name)
	case status == types.GitHubIdle:
		return types.Runner{}, errdefs.InvalidArgument("runner %q has no machine on the fleet, but is connected "+
			"to GitHub: its machine is on a provider that could not be listed, or is another installation's", name)
	}

	ref, err := m.github.GetRunnerByName(ctx, name)
	switch {
	case err != nil:
		return types.Runner{}, fmt.Errorf("look up runner %q on GitHub: %w", name, err)
	case ref == nil:
		return types.Runner{}, errdefs.NotFound("no runner %q on the fleet or on GitHub", name)
	}

	i := slices.IndexFunc(m.sets, func(s *scaleSet) bool { return s.hasRegistration(ref) })
	if i < 0 {
		return types.Runner{}, errdefs.NotFound("no runner %q on the fleet, and GitHub has it in none of the "+
			"configured scale sets", name)
	}

	s := m.sets[i]
	r := types.Runner{Name: name, ScaleSet: s.spec.Name, GitHubStatus: status}

	return r, s.removeOrphanOnRequest(ctx, ref)
}

// removeOrphanOnRequest removes an orphan's registration on request, as
// removeOrphan does. It returns checkHeld's error unless the scale set holds
// its session.
func (s *scaleSet) removeOrphanOnRequest(ctx context.Context, ref *ghscaleset.RunnerReference) error {
	// Held throughout, so that the session is not lost and the runners
	// forgotten between the check and the removal.
	s.passLock.Lock()
	defer s.passLock.Unlock()

	if err := s.checkHeld(); err != nil {
		return err
	}

	return s.removeOrphan(ctx, ref, types.RemovalRequested)
}

// removeOrphans removes the scale set's registrations that have no machine,
// such as those left when a host is lost before its runners are adopted. One
// is removed once offline and unknown for longer than the start timeout. Call
// it only when every provider has been listed or written off.
func (s *scaleSet) removeOrphans(ctx context.Context, now time.Time) {
	offline, err := s.gitHubStatuses.OfflineRunners(ctx)
	if err != nil {
		s.logger.Debug("cannot list GitHub's runners to find registrations with no machine",
			slog.Any("error", err))

		return
	}

	var due []string

	s.mu.Lock()
	found := make(map[string]bool, len(offline))
	for _, name := range offline {
		if s.tracks(name) || !isRunnerNameOf(s.spec.Name, name) {
			continue
		}

		found[name] = true
		since, ok := s.orphans[name]
		switch {
		case !ok:
			s.orphans[name] = now
		case now.Sub(since) > s.spec.StartTimeout:
			due = append(due, name)
		}
	}
	for name := range s.orphans {
		if !found[name] {
			delete(s.orphans, name)
		}
	}
	s.mu.Unlock()

	for _, name := range due {
		ref, err := s.github.GetRunnerByName(ctx, name)
		switch {
		case err != nil:
			s.logger.Debug("cannot look up a registration with no machine",
				slog.String("runner", name), slog.Any("error", err))

			continue
		case ref == nil || !s.hasRegistration(ref):
			continue
		}

		if err := s.removeOrphan(ctx, ref, types.RemovalOrphaned); err != nil {
			s.logger.Warn("cannot remove the registration of a runner with no machine",
				slog.String("runner", name), slog.Any("error", err))
		}
	}
}

// hasRegistration reports whether ref belongs to the scale set.
func (s *scaleSet) hasRegistration(ref *ghscaleset.RunnerReference) bool {
	id := s.id.Load()
	return id != 0 && int64(ref.RunnerScaleSetID) == id
}

// removeOrphan removes ref, a registration with no machine, counting it under
// reason. It refuses a runner the scale set knows, whose machine is being
// created or on a provider that cannot be listed, and one leaving, which
// settles its own registration.
func (s *scaleSet) removeOrphan(ctx context.Context, ref *ghscaleset.RunnerReference,
	reason types.RemovalReason,
) error {
	s.mu.Lock()
	_, known := s.runners[ref.Name]
	_, leaving := s.leaving[ref.Name]
	creating := s.creating[ref.Name]
	s.mu.Unlock()

	switch {
	case creating:
		return errdefs.Unavailable("runner %q is being created", ref.Name)
	case leaving:
		return errdefs.Unavailable("runner %q is leaving scale set %q, which removes its registration",
			ref.Name, s.spec.Name)
	case known:
		return errdefs.Unavailable("runner %q is not on the fleet, but scale set %q has it: its provider may "+
			"be unreachable; try again once it is reachable", ref.Name, s.spec.Name)
	}

	err := s.github.RemoveRunner(ctx, int64(ref.ID))
	switch {
	case errors.Is(err, ghscaleset.JobStillRunningError):
		return errdefs.Busy("runner %q is running a job", ref.Name)
	case err != nil:
		return fmt.Errorf("remove the registration of runner %q: %w", ref.Name, err)
	}

	s.mu.Lock()
	delete(s.orphans, ref.Name)
	s.mu.Unlock()

	s.metrics.CountRunnerRemoved(s.spec.Name, "", reason)
	s.logger.Warn("removed the registration of a runner with no machine",
		slog.String("runner", ref.Name), slog.String("reason", string(reason)))
	s.events.Record(registrationRemovedEvent(s.spec.Name, ref.Name, reason))

	return nil
}
