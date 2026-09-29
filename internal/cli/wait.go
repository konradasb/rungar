// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// pollInterval is how often providers drain, and scale-sets pause --wait and
// rm --wait, look again.
var pollInterval = 5 * time.Second

// waitForNoRunners polls until req lists no runners, writing how many are
// left whenever that changes, and done once none are. It keeps waiting while
// any provider in providers cannot be listed, since runners there may remain.
// where says whose runners they are: "on compute2".
func waitForNoRunners(ctx context.Context, out io.Writer, client rungarv1.RungarServiceClient,
	req *rungarv1.ListRunnersRequest, providers []string, where, done string,
) error {
	last := -1

	for {
		resp, err := client.ListRunners(ctx, req)
		if err != nil {
			return err
		}

		var unreachable []string
		for _, name := range providers {
			if _, ok := resp.GetUnreachableProviders()[name]; ok {
				unreachable = append(unreachable, name)
			}
		}

		switch n := len(resp.GetRunners()); {
		case len(unreachable) > 0:
			_, _ = fmt.Fprintf(out, "%s cannot be listed; trying again\n", strings.Join(unreachable, ", "))
		case n == 0:
			_, _ = fmt.Fprintln(out, done)
			return nil
		case n != last:
			_, _ = fmt.Fprintf(out, "Waiting for %s %s to finish\n", plural(n, "runner"), where)
			last = n
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}
