// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"io"
	"slices"

	"github.com/spf13/cobra"

	"github.com/konradasb/rungar/internal/errdefs"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// scaleSetChange is what rungar scale-sets pause or resume does.
type scaleSetChange struct {
	pause bool // pause the scale set, rather than resume it
	wait  bool // then wait until no runner of it is left
}

func newScaleSetPauseCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pause NAME",
		Short: "Stop a scale set taking jobs, leaving its busy runners to finish",
		Long: "Stop a scale set taking jobs: GitHub is told it has no room, no runner is created for it, not " +
			"even its min_runners, and its idle runners are removed; those running jobs finish them and " +
			"go. The jobs that target it wait on GitHub until it is resumed, and GitHub cancels one left " +
			"queued for a day. --wait waits until its last runner has gone." + untilRestart("paused"),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			wait, _ := cmd.Flags().GetBool("wait")

			return withClient(cmd, func(client rungarv1.RungarServiceClient) error {
				return changeScaleSet(cmd.Context(), cmd.OutOrStdout(), client, args[0],
					scaleSetChange{pause: true, wait: wait})
			})
		},
	}
	cmd.Flags().Bool("wait", false, "Wait until the scale set's last runner has gone")

	return cmd
}

func newScaleSetResumeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "resume NAME",
		Short: "Have a paused scale set take jobs again",
		Long: "Have a paused scale set take jobs again: its min_runners are created at once, and the jobs " +
			"waiting for it are taken within a minute, as GitHub next hears it has room." +
			untilRestart("paused"),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd, func(client rungarv1.RungarServiceClient) error {
				return changeScaleSet(cmd.Context(), cmd.OutOrStdout(), client, args[0], scaleSetChange{})
			})
		},
	}
}

// changeScaleSet has the daemon make the change to a scale set, and waits for
// its runners to go if asked to.
func changeScaleSet(ctx context.Context, out io.Writer, client rungarv1.RungarServiceClient, name string,
	change scaleSetChange,
) error {
	st, err := client.GetStatus(ctx, &rungarv1.GetStatusRequest{})
	if err != nil {
		return err
	}

	set, err := findScaleSet(st, name)
	if err != nil {
		return err
	}

	already := set.GetPaused() == change.pause
	if !already {
		if change.pause {
			_, err = client.PauseScaleSet(ctx, &rungarv1.PauseScaleSetRequest{Name: name})
		} else {
			_, err = client.ResumeScaleSet(ctx, &rungarv1.ResumeScaleSetRequest{Name: name})
		}
		if err != nil {
			return err
		}
	}
	writeChange(out, "Scale set", name, pausedOrResumed(change.pause), already,
		change.pause == set.GetConfiguredPaused())

	if change.pause && change.wait {
		return waitForNoRunners(ctx, out, client, &rungarv1.ListRunnersRequest{ScaleSet: name}, set.GetProviders(),
			"of "+name, name+" is paused, and no runner of it is left")
	}

	return nil
}

// findScaleSet returns the configured scale set of this name in st, or an
// errdefs.ErrNotFound naming those there are.
func findScaleSet(st *rungarv1.DaemonStatus, name string) (*rungarv1.ScaleSet, error) {
	sets := st.GetScaleSets()

	i := slices.IndexFunc(sets, func(set *rungarv1.ScaleSet) bool { return set.GetName() == name })
	if i >= 0 {
		return sets[i], nil
	}

	names := make([]string, 0, len(sets))
	for _, set := range sets {
		names = append(names, set.GetName())
	}

	return nil, errdefs.NotFound("no scale set %q in the configuration; it has %s", name, configuredNames(names))
}

// pausedOrResumed returns the state pausing, or else resuming, leaves a scale
// set in.
func pausedOrResumed(pause bool) string {
	if pause {
		return "paused"
	}

	return "resumed"
}
