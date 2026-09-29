// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/github"
	"github.com/konradasb/rungar/internal/types"
)

// TestGitHubRunnerCacheSharesOneListing checks GitHub is listed once for every
// runner asked about while the listing is fresh, and again once it is not.
func TestGitHubRunnerCacheSharesOneListing(t *testing.T) {
	var lists int
	now := time.Now()

	r := &gitHubRunnerCache{
		listRunners: func(context.Context) ([]github.Runner, error) {
			lists++
			return []github.Runner{
				{Name: "busy", Status: "online", Busy: true},
				{Name: "idle", Status: "online"},
				{Name: "gone", Status: "offline"},
			}, nil
		},
		now: func() time.Time { return now },
	}
	ctx := context.Background()

	for name, want := range map[string]types.GitHubStatus{
		"busy":    types.GitHubBusy,
		"idle":    types.GitHubIdle,
		"gone":    types.GitHubOffline,
		"nowhere": types.GitHubNotRegistered,
	} {
		if got, err := r.GitHubStatus(ctx, name); err != nil || got != want {
			t.Errorf("GitHubStatus(%s) = %q, %v; want %q", name, got, err, want)
		}
	}
	if lists != 1 {
		t.Errorf("GitHub was listed %d times, want once: the listing is shared", lists)
	}

	now = now.Add(gitHubRunnerCacheMaxAge)
	_, _ = r.GitHubStatus(ctx, "idle")
	if lists != 2 {
		t.Errorf("GitHub was listed %d times, want again once the listing is old", lists)
	}
}
