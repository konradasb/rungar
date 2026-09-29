// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// Words of a runner's STATE, GITHUB and MACHINE columns.
const (
	runnerStarting = "Starting"
	runnerIdle     = "Idle"
	runnerBusy     = "Busy"

	gitHubBusy         = "Busy"
	gitHubIdle         = "Idle"
	gitHubOffline      = "Offline"
	gitHubUnregistered = "Not registered"

	machineRunning = "Running"
	machineStopped = "Stopped"
)

func newRunnersCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "runners",
		Aliases: []string{"runner"},
		Short:   "List and remove runners",
		Args:    cobra.NoArgs,
	}

	cmd.AddCommand(newRunnerListCommand(), newRunnerRemoveCommand())

	return cmd
}

func newRunnerListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List runners: their machines, what the daemon knows of them, and what GitHub says",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			asJSON, _ := cmd.Flags().GetBool("json")
			scaleSet, _ := cmd.Flags().GetString("scale-set")
			provider, _ := cmd.Flags().GetString("provider")

			return withClient(cmd, func(client rungarv1.RungarServiceClient) error {
				return listRunners(cmd, client, scaleSet, provider, asJSON)
			})
		},
	}
	cmd.Flags().Bool("json", false, "Write JSON")
	cmd.Flags().String("scale-set", "", "Only this scale set's runners")
	cmd.Flags().String("provider", "", "Only the runners on this provider")

	return cmd
}

func newRunnerRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "rm NAME...",
		Aliases: []string{"remove", "delete"},
		Short:   "Remove runners, the safe way round",
		Long: "Remove runners, the safe way round: each one's registration first, which GitHub refuses " +
			"for a runner running a job, and only then its machine. A runner running a job is left to " +
			"finish, and said so. A scale set whose runner is removed makes another if it needs one.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd, func(client rungarv1.RungarServiceClient) error {
				return removeRunners(cmd, client, args)
			})
		},
	}
}

// listRunners writes the installation's runners, only those of scaleSet or on
// provider if either is set.
func listRunners(cmd *cobra.Command, client rungarv1.RungarServiceClient, scaleSet, provider string,
	asJSON bool,
) error {
	resp, err := client.ListRunners(cmd.Context(), &rungarv1.ListRunnersRequest{ScaleSet: scaleSet, Provider: provider})
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if asJSON {
		return writeJSON(out, resp)
	}

	p := paletteFor(out)
	writeRunners(out, p, resp.GetRunners(), 0)
	writeListingNotes(out, p, 0, resp.GetUnreachableProviders(), resp.GetGithubError())

	return nil
}

// removeRunners has the daemon remove each named runner in turn, reporting
// each one it could not remove, and fails if any was not.
func removeRunners(cmd *cobra.Command, client rungarv1.RungarServiceClient, names []string) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	var failed int

	for _, name := range names {
		r, err := client.DeleteRunner(ctx, &rungarv1.DeleteRunnerRequest{Name: name})
		if err != nil {
			if ctx.Err() != nil {
				return err
			}

			failed++
			_, _ = fmt.Fprintf(out, "%s: %s\n", name, errorMessage(err))

			continue
		}

		_, _ = fmt.Fprintf(out, "%s: removed from %s\n", name, r.GetProvider())
	}

	if failed > 0 {
		return fmt.Errorf("%s not removed", plural(failed, "runner"))
	}

	return nil
}

// writeRunners writes a row per runner, indented, or says there are none.
func writeRunners(w io.Writer, p palette, runners []*rungarv1.Runner, indent int) {
	if len(runners) == 0 {
		_, _ = fmt.Fprintf(w, "%s%s\n", strings.Repeat(" ", indent), p.dim("none on the fleet right now"))

		return
	}

	t := newTable("RUNNER", "SCALE SET", "PROVIDER", "STATE", "MACHINE", "GITHUB", "AGE").indented(indent)

	now := time.Now()
	for _, r := range runners {
		t.row(r.GetName(), p.accent(r.GetScaleSet()), r.GetProvider(), p.status(stateWord(r.GetState())),
			p.status(machineWord(r.GetMachineState())), p.status(gitHubStatusWord(r.GetGithubStatus())),
			ageSince(now, r.GetCreateTime()))
	}

	t.write(w, p)
}

// writeListingNotes writes, indented, what a runner listing is missing: the
// providers that could not be listed, and GitHub if gitHubErr is set.
func writeListingNotes(w io.Writer, p palette, indent int, unreachable map[string]string, gitHubErr string) {
	prefix := strings.Repeat(" ", indent)

	for _, name := range slices.Sorted(maps.Keys(unreachable)) {
		_, _ = fmt.Fprintf(w, "%s%s\n", prefix,
			p.dim(fmt.Sprintf("Runners on %s are not listed: %s", name, unreachable[name])))
	}

	if gitHubErr != "" {
		_, _ = fmt.Fprintf(w, "%s%s\n", prefix, p.dim("GitHub could not be asked what the runners are doing: "+gitHubErr))
	}
}

// stateWord returns a runner's STATE column.
func stateWord(s rungarv1.RunnerState) string {
	switch s {
	case rungarv1.RunnerState_RUNNER_STATE_STARTING:
		return runnerStarting
	case rungarv1.RunnerState_RUNNER_STATE_IDLE:
		return runnerIdle
	case rungarv1.RunnerState_RUNNER_STATE_BUSY:
		return runnerBusy
	default:
		return "-"
	}
}

// gitHubStatusWord returns a runner's GITHUB column.
func gitHubStatusWord(s rungarv1.RunnerGitHubStatus) string {
	switch s {
	case rungarv1.RunnerGitHubStatus_RUNNER_GIT_HUB_STATUS_BUSY:
		return gitHubBusy
	case rungarv1.RunnerGitHubStatus_RUNNER_GIT_HUB_STATUS_IDLE:
		return gitHubIdle
	case rungarv1.RunnerGitHubStatus_RUNNER_GIT_HUB_STATUS_OFFLINE:
		return gitHubOffline
	case rungarv1.RunnerGitHubStatus_RUNNER_GIT_HUB_STATUS_NOT_REGISTERED:
		return gitHubUnregistered
	default:
		return "-"
	}
}

// machineWord returns a runner's MACHINE column: its machine's state,
// capitalised.
func machineWord(state string) string {
	if state == "" {
		return "-"
	}

	return strings.ToUpper(state[:1]) + state[1:]
}
