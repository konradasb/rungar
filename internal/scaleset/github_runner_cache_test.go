// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"sync"
	"sync/atomic"
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

	c := &gitHubRunnerCache{
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
		t.Run(name, func(t *testing.T) {
			if got, err := c.GitHubStatus(ctx, name); err != nil || got != want {
				t.Errorf("GitHubStatus(%s) = %q, %v; want %q", name, got, err, want)
			}
		})
	}
	if lists != 1 {
		t.Errorf("GitHub was listed %d times, want once: the listing is shared", lists)
	}

	now = now.Add(gitHubRunnerCacheMaxAge)
	_, _ = c.GitHubStatus(ctx, "idle")
	if lists != 2 {
		t.Errorf("GitHub was listed %d times, want again once the listing is old", lists)
	}
}

// TestGitHubRunnerCacheConcurrentCallersShareAListing checks callers asking
// while GitHub is being listed wait for that listing rather than each listing
// again.
func TestGitHubRunnerCacheConcurrentCallersShareAListing(t *testing.T) {
	var lists atomic.Int32
	listing, release := make(chan struct{}), make(chan struct{})

	c := newGitHubRunnerCache(listerFunc(func(context.Context) ([]github.Runner, error) {
		if lists.Add(1) == 1 {
			close(listing)
		}
		<-release

		return []github.Runner{{Name: "idle", Status: "online"}}, nil
	}))
	ctx := context.Background()

	const callers = 5

	var wg sync.WaitGroup
	statuses := make(chan types.GitHubStatus, callers)
	wg.Go(func() {
		status, _ := c.GitHubStatus(ctx, "idle")
		statuses <- status
	})
	<-listing
	for range callers - 1 {
		wg.Go(func() {
			status, _ := c.GitHubStatus(ctx, "idle")
			statuses <- status
		})
	}

	// A caller arriving after the listing finds it cached, which is shared
	// too.
	close(release)
	wg.Wait()
	close(statuses)

	for status := range statuses {
		if status != types.GitHubIdle {
			t.Errorf("GitHubStatus() = %q, want %q", status, types.GitHubIdle)
		}
	}
	if got := lists.Load(); got != 1 {
		t.Errorf("GitHub was listed %d times, want once for every caller", got)
	}
}

// listerFunc is a GitHubRunnerLister answering with a function.
type listerFunc func(ctx context.Context) ([]github.Runner, error)

func (f listerFunc) Runners(ctx context.Context) ([]github.Runner, error) { return f(ctx) }
