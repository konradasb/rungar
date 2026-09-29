// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"sync"
	"time"

	"github.com/konradasb/rungar/internal/github"
	"github.com/konradasb/rungar/internal/types"
)

// gitHubRunnerCacheMaxAge is how long a listing of GitHub's runners is reused.
const gitHubRunnerCacheMaxAge = 15 * time.Second

// gitHubRunnerCache caches GitHub's listing of every runner in the scope, for
// the scale sets to share. GitHub lists runners only all together, so asking
// about each would spend the rate limit on the same listing over and over.
type gitHubRunnerCache struct {
	listRunners func(ctx context.Context) ([]github.Runner, error)

	// now is time.Now, replaced in tests.
	now func() time.Time

	// mu is held while listing, so that concurrent callers share one.
	mu       sync.Mutex
	byName   map[string]types.GitHubStatus
	listedAt time.Time
}

func newGitHubRunnerCache(lister GitHubRunnerLister) *gitHubRunnerCache {
	return &gitHubRunnerCache{listRunners: lister.ListRunners, now: time.Now}
}

// GitHubStatus returns GitHub's status for the runner of this name.
func (r *gitHubRunnerCache) GitHubStatus(ctx context.Context, name string) (types.GitHubStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.refreshIfStaleLocked(ctx); err != nil {
		return "", err
	}

	if status, ok := r.byName[name]; ok {
		return status, nil
	}

	return types.GitHubNotRegistered, nil
}

// RefreshIfStale lists GitHub's runners unless the cached listing is fresh,
// returning GitHub's error if it does not answer.
func (r *gitHubRunnerCache) RefreshIfStale(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.refreshIfStaleLocked(ctx)
}

// refreshIfStaleLocked is RefreshIfStale with r.mu held.
func (r *gitHubRunnerCache) refreshIfStaleLocked(ctx context.Context) error {
	if r.byName != nil && r.now().Sub(r.listedAt) < gitHubRunnerCacheMaxAge {
		return nil
	}

	runners, err := r.listRunners(ctx)
	if err != nil {
		return err
	}

	r.byName = make(map[string]types.GitHubStatus, len(runners))
	for _, gr := range runners {
		r.byName[gr.Name] = gitHubStatusOf(gr)
	}
	r.listedAt = r.now()

	return nil
}

// gitHubStatusOf returns a listed runner's status.
func gitHubStatusOf(r github.Runner) types.GitHubStatus {
	switch {
	case r.Busy:
		return types.GitHubBusy
	case r.Online():
		return types.GitHubIdle
	default:
		return types.GitHubOffline
	}
}
