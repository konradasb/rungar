// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/types"
)

func TestShouldRemove(t *testing.T) {
	now := time.Now()
	spec := types.ScaleSetSpec{
		StartTimeout:    5 * time.Minute,
		RunnerRevisions: map[string]string{"a": "current"},
	}
	aging := spec
	aging.MaxIdleAge = time.Hour
	aging.MaxAge = 24 * time.Hour

	runner := func(state types.RunnerState, age time.Duration) types.Runner {
		return types.Runner{Provider: "a", State: state, Revision: "current", CreatedAt: now.Add(-age)}
	}
	outdated := runner(types.RunnerIdle, time.Minute)
	outdated.Revision = "older"
	unknownAge := runner(types.RunnerIdle, 0)
	unknownAge.CreatedAt = time.Time{}

	tests := []struct {
		name    string
		spec    types.ScaleSetSpec
		runner  types.Runner
		status  types.GitHubStatus
		offline time.Duration
		want    removal
	}{
		{name: "idle and connected", spec: spec, runner: runner(types.RunnerIdle, time.Hour),
			status: types.GitHubIdle},
		{name: "starting within its timeout", spec: spec, runner: runner(types.RunnerStarting, time.Minute),
			status: types.GitHubOffline},
		{name: "never connected", spec: spec, runner: runner(types.RunnerStarting, 10*time.Minute),
			status: types.GitHubOffline, want: removal{reason: types.RemovalNeverConnected}},
		{name: "starting and not yet registered", spec: spec, runner: runner(types.RunnerStarting, time.Minute),
			status: types.GitHubNotRegistered},
		{name: "unregistered", spec: spec, runner: runner(types.RunnerIdle, time.Minute),
			status: types.GitHubNotRegistered, want: removal{reason: types.RemovalUnregistered}},
		{name: "briefly disconnected", spec: spec, runner: runner(types.RunnerIdle, time.Hour),
			status: types.GitHubOffline, offline: time.Minute},
		{name: "disconnected", spec: spec, runner: runner(types.RunnerIdle, time.Hour),
			status: types.GitHubOffline, offline: 10 * time.Minute, want: removal{reason: types.RemovalDisconnected}},
		{name: "outdated", spec: spec, runner: outdated, status: types.GitHubIdle,
			want: removal{reason: types.RemovalOutdated, replacement: true}},
		{name: "old, with no max_idle_age", spec: spec, runner: runner(types.RunnerIdle, 48*time.Hour),
			status: types.GitHubIdle},
		{name: "older than max_idle_age", spec: aging, runner: runner(types.RunnerIdle, 2*time.Hour),
			status: types.GitHubIdle, want: removal{reason: types.RemovalExpired, replacement: true}},
		{name: "of unknown age", spec: aging, runner: unknownAge, status: types.GitHubIdle},
		{name: "busy and connected", spec: aging, runner: runner(types.RunnerBusy, 2*time.Hour),
			status: types.GitHubBusy},
		{name: "busy by GitHub alone", spec: aging, runner: runner(types.RunnerIdle, 2*time.Hour),
			status: types.GitHubBusy},
		{name: "busy and outdated", spec: spec, runner: func() types.Runner {
			r := outdated
			r.State = types.RunnerBusy
			return r
		}(), status: types.GitHubBusy},
		{name: "busy and briefly disconnected", spec: spec, runner: runner(types.RunnerBusy, time.Hour),
			status: types.GitHubNotRegistered, offline: time.Minute},
		{name: "stuck", spec: spec, runner: runner(types.RunnerBusy, time.Hour),
			status: types.GitHubOffline, offline: 10 * time.Minute, want: removal{reason: types.RemovalStuck, force: true}},
		{name: "busy past max_age", spec: aging, runner: runner(types.RunnerBusy, 25*time.Hour),
			status: types.GitHubBusy, want: removal{reason: types.RemovalExpired, force: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := shouldRemove(tt.spec, tt.runner, tt.status, tt.offline, now)
			if got != tt.want || ok != (tt.want.reason != "") {
				t.Errorf("shouldRemove() = %+v, %t; want %+v", got, ok, tt.want)
			}
		})
	}
}
