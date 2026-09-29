// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/konradasb/rungar/internal/github"
	"github.com/konradasb/rungar/internal/types"
)

// gitHubRunnerCacheMaxAge is how long a listing of GitHub's runners is reused.
const gitHubRunnerCacheMaxAge = 15 * time.Second

// gitHubRunnerCache caches GitHub's listing of every runner in the scope, for
// the scale sets to share. GitHub lists runners only all together, so asking
// about each would spend the rate limit on the same listing over and over. Its
// methods are safe for concurrent use.
type gitHubRunnerCache struct {
	listRunners func(ctx context.Context) ([]github.Runner, error)

	// now is time.Now, replaced in tests.
	now func() time.Time

	// listing has concurrent callers share one listing of GitHub's runners.
	listing singleflight.Group

	// mu guards the fields below. It is never held while GitHub is asked.
	// byName is replaced by each listing, never changed in place.
	mu       sync.Mutex
	byName   map[string]types.GitHubStatus
	listedAt time.Time
}

// newGitHubRunnerCache returns a cache of lister's runners that has listed
// nothing yet: it first asks GitHub when first asked itself.
func newGitHubRunnerCache(lister GitHubRunnerLister) *gitHubRunnerCache {
	return &gitHubRunnerCache{listRunners: lister.Runners, now: time.Now}
}

// GitHubStatus returns GitHub's status for the runner of this name.
func (c *gitHubRunnerCache) GitHubStatus(ctx context.Context, name string) (types.GitHubStatus, error) {
	byName, err := c.statuses(ctx)
	if err != nil {
		return "", err
	}

	if status, ok := byName[name]; ok {
		return status, nil
	}

	return types.GitHubNotRegistered, nil
}

// OfflineRunners returns the names of the runners GitHub has disconnected.
func (c *gitHubRunnerCache) OfflineRunners(ctx context.Context) ([]string, error) {
	byName, err := c.statuses(ctx)
	if err != nil {
		return nil, err
	}

	var names []string
	for name, status := range byName {
		if status == types.GitHubOffline {
			names = append(names, name)
		}
	}

	return names, nil
}

// RefreshIfStale lists GitHub's runners unless the cached listing is fresh,
// returning GitHub's error if it does not answer.
func (c *gitHubRunnerCache) RefreshIfStale(ctx context.Context) error {
	_, err := c.statuses(ctx)
	return err
}

// statuses returns each runner's status by name, listing GitHub's runners
// unless the cached listing is fresh. Callers asking while a listing is under
// way share it. The map returned must not be changed.
func (c *gitHubRunnerCache) statuses(ctx context.Context) (map[string]types.GitHubStatus, error) {
	for retried := false; ; retried = true {
		if byName := c.fresh(); byName != nil {
			return byName, nil
		}

		listed := c.listing.DoChan("", func() (any, error) { return c.list(ctx) })

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case res := <-listed:
			// A listing shared with a caller that gave up is tried again, once.
			if !retried && res.Shared && ctx.Err() == nil && isContextError(res.Err) {
				continue
			}
			if res.Err != nil {
				return nil, res.Err
			}

			byName, _ := res.Val.(map[string]types.GitHubStatus)

			return byName, nil
		}
	}
}

// fresh returns the cached statuses, or nil if they are missing or stale.
func (c *gitHubRunnerCache) fresh() map[string]types.GitHubStatus {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.byName == nil || c.now().Sub(c.listedAt) >= gitHubRunnerCacheMaxAge {
		return nil
	}

	return c.byName
}

// list lists GitHub's runners and caches their statuses, unless a listing
// that ended since its caller looked has cached them already.
func (c *gitHubRunnerCache) list(ctx context.Context) (map[string]types.GitHubStatus, error) {
	if byName := c.fresh(); byName != nil {
		return byName, nil
	}

	runners, err := c.listRunners(ctx)
	if err != nil {
		return nil, err
	}

	byName := make(map[string]types.GitHubStatus, len(runners))
	for _, r := range runners {
		byName[r.Name] = gitHubStatusOf(r)
	}

	c.mu.Lock()
	c.byName, c.listedAt = byName, c.now()
	c.mu.Unlock()

	return byName, nil
}

// isContextError reports whether err is a context's, cancelled or past its
// deadline.
func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
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
