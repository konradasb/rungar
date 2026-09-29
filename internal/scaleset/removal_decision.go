// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"time"

	"github.com/konradasb/rungar/internal/types"
)

// removal is reconciliation's decision to remove a runner.
type removal struct {
	reason types.RemovalReason

	// force removes the runner whatever GitHub says, even while it runs a job.
	force bool

	// replacement marks the removal of a runner that could still take a job.
	// Reconciliation makes at most one per pass, so as not to empty the
	// reserve at once.
	replacement bool
}

// decideRemoval decides whether reconciliation removes a runner whose machine
// is alive, given GitHub's status for it and how long GitHub has had it
// disconnected.
//
// A runner running a job is removed only when it is older than max_age, or has
// been disconnected for longer than the start timeout. Any other is removed
// when it can no longer take a job, and replaced when it was created from
// another revision of the runner spec or is older than max_idle_age.
func decideRemoval(spec types.ScaleSetSpec, r types.Runner, status types.GitHubStatus, offline time.Duration,
	now time.Time,
) (removal, bool) {
	age := now.Sub(r.CreatedAt)
	olderThan := func(limit time.Duration) bool {
		return limit > 0 && !r.CreatedAt.IsZero() && age > limit
	}
	young := r.State == types.RunnerStarting && age <= spec.StartTimeout
	connected := status == types.GitHubIdle || status == types.GitHubBusy

	if r.State == types.RunnerBusy || status == types.GitHubBusy {
		switch {
		case olderThan(spec.MaxAge):
			return removal{reason: types.RemovalExpired, force: true}, true
		case !connected && offline > spec.StartTimeout:
			return removal{reason: types.RemovalStuck, force: true}, true
		}

		return removal{}, false
	}

	switch {
	case status == types.GitHubNotRegistered && !young:
		return removal{reason: types.RemovalUnregistered}, true
	case !connected && r.State == types.RunnerStarting && !young:
		return removal{reason: types.RemovalNeverConnected}, true
	case !connected && offline > spec.StartTimeout:
		return removal{reason: types.RemovalDisconnected}, true
	case r.Revision != spec.RunnerRevisions[r.Provider]:
		return removal{reason: types.RemovalOutdated, replacement: true}, true
	case olderThan(spec.MaxIdleAge), olderThan(spec.MaxAge):
		return removal{reason: types.RemovalExpired, replacement: true}, true
	}

	return removal{}, false
}
