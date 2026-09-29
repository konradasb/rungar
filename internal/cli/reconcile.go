// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

func newReconcileCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "reconcile [SCALE_SET...]",
		Short: "Have the daemon compare scale sets with the fleet now",
		Long: "Have the daemon compare the named scale sets, or every one, with the fleet now rather " +
			"than at its next reconcile_interval: forget runners whose machines are gone, remove those " +
			"that never connected or have stopped, and make up any it is short of. It returns once it " +
			"has, and says what each has then.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd, func(client rungarv1.RungarServiceClient) error {
				return reconcile(cmd, client, args)
			})
		},
	}
}

// reconcile has the daemon reconcile the named scale sets, or all of them, and
// writes what each has afterwards.
func reconcile(cmd *cobra.Command, client rungarv1.RungarServiceClient, names []string) error {
	resp, err := client.Reconcile(cmd.Context(), &rungarv1.ReconcileRequest{ScaleSets: names})
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	p := paletteFor(out)

	for _, set := range resp.GetScaleSets() {
		_, _ = fmt.Fprintf(out, "%s: %s, %d desired\n", p.accent(set.GetName()),
			plural(runnerTotal(set.GetRunnerCounts()), "runner"), set.GetDesiredRunners())
	}

	return nil
}
