// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/konradasb/rungar/internal/errdefs"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// scaleSetChangeLasts ends the long description of pause and resume.
const scaleSetChangeLasts = " It lasts until the daemon restarts, which goes back to what the configuration " +
	"says: set paused there to make it last."

func newScaleSetPauseCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pause NAME",
		Short: "Stop a scale set taking jobs, leaving its busy runners to finish",
		Long: "Stop a scale set taking jobs: GitHub is told it has no room, no runner is made for it, not " +
			"even its min_runners, and its idle runners are removed; those running jobs finish them and " +
			"go. The jobs that target it wait on GitHub until it is resumed, and GitHub cancels one left " +
			"queued for a day. --wait waits until its last runner has gone." + scaleSetChangeLasts,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			wait, _ := cmd.Flags().GetBool("wait")

			return withClient(cmd, func(client rungarv1.RungarServiceClient) error {
				return changeScaleSet(cmd, client, args[0], true, wait)
			})
		},
	}
	cmd.Flags().Bool("wait", false, "Wait until the scale set's last runner has gone")

	return cmd
}

func newScaleSetResumeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resume NAME",
		Short: "Have a paused scale set take jobs again",
		Long: "Have a paused scale set take jobs again: its min_runners are made at once, and the jobs " +
			"waiting for it are taken within a minute, as GitHub next hears it has room." + scaleSetChangeLasts,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd, func(client rungarv1.RungarServiceClient) error {
				return changeScaleSet(cmd, client, args[0], false, false)
			})
		},
	}

	return cmd
}

// changeScaleSet has the daemon pause or resume a scale set and, paused, waits
// for its runners to go if asked to.
func changeScaleSet(cmd *cobra.Command, client rungarv1.RungarServiceClient, name string, pause, wait bool) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	st, err := client.GetStatus(ctx, &rungarv1.GetStatusRequest{})
	if err != nil {
		return err
	}

	set, err := findScaleSet(st, name)
	if err != nil {
		return err
	}

	asConfigured := pause == set.GetConfiguredPaused()

	if set.GetPaused() == pause {
		_, _ = fmt.Fprintf(out, "Scale set %s is already %s.\n", name, pausedWord(pause))
	} else {
		if pause {
			_, err = client.PauseScaleSet(ctx, &rungarv1.PauseScaleSetRequest{Name: name})
		} else {
			_, err = client.ResumeScaleSet(ctx, &rungarv1.ResumeScaleSetRequest{Name: name})
		}
		if err != nil {
			return err
		}

		if asConfigured {
			_, _ = fmt.Fprintf(out, "Scale set %s is %s, as the configuration has it.\n", name, pausedWord(pause))
		} else {
			_, _ = fmt.Fprintf(out, "Scale set %s is %s until the daemon restarts.\n", name, pausedWord(pause))
		}
	}

	if pause && wait {
		return waitForNoRunners(cmd, client, &rungarv1.ListRunnersRequest{ScaleSet: name}, set.GetProviders(),
			"of "+name, name+" is paused, and no runner of it is left")
	}

	return nil
}

// findScaleSet returns the configured scale set of this name in st.
func findScaleSet(st *rungarv1.DaemonStatus, name string) (*rungarv1.ScaleSet, error) {
	sets := st.GetScaleSets()

	i := slices.IndexFunc(sets, func(s *rungarv1.ScaleSet) bool { return s.GetName() == name })
	if i >= 0 {
		return sets[i], nil
	}

	if len(sets) == 0 {
		return nil, errdefs.NotFound("no scale set %q in the configuration; it has none", name)
	}

	names := make([]string, 0, len(sets))
	for _, s := range sets {
		names = append(names, s.GetName())
	}

	return nil, errdefs.NotFound("no scale set %q in the configuration; it has %s", name, strings.Join(names, ", "))
}

func pausedWord(paused bool) string {
	if paused {
		return "paused"
	}

	return "resumed"
}
