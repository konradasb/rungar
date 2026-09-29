// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
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
		Short:   "List runners: their machines, what the daemon knows of them, what GitHub says, and their jobs",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			asJSON, _ := cmd.Flags().GetBool("json")
			scaleSet, _ := cmd.Flags().GetString("scale-set")
			provider, _ := cmd.Flags().GetString("provider")

			return withClient(cmd, func(client rungarv1.RungarServiceClient) error {
				return listRunners(cmd.Context(), cmd.OutOrStdout(), client, scaleSet, provider, asJSON)
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
			"finish, and said so. A scale set whose runner is removed creates another if it needs one. " +
			"A runner of a configured scale set with no machine on the fleet, disconnected from GitHub, " +
			"has its registration removed.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd, func(client rungarv1.RungarServiceClient) error {
				return removeRunners(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), client, args)
			})
		},
	}
}

// listRunners writes the installation's runners, only those of scaleSet or on
// provider if either is set.
func listRunners(ctx context.Context, out io.Writer, client rungarv1.RungarServiceClient,
	scaleSet, provider string, asJSON bool,
) error {
	resp, err := client.ListRunners(ctx, &rungarv1.ListRunnersRequest{ScaleSet: scaleSet, Provider: provider})
	if err != nil {
		return err
	}

	if asJSON {
		return writeJSON(out, resp)
	}

	p := paletteFor(out)
	writeRunners(out, p, resp.GetRunners(), time.Now(), 0)
	writeListingNotes(out, p, 0, resp.GetUnreachableProviders(), resp.GetGithubError())

	return nil
}

// removeRunners has the daemon remove each named runner in turn, writing
// each one removed to out and each one it could not remove to errOut, and
// fails if any was not.
func removeRunners(ctx context.Context, out, errOut io.Writer, client rungarv1.RungarServiceClient,
	names []string,
) error {
	var failed int

	for _, name := range names {
		r, err := client.RemoveRunner(ctx, &rungarv1.RemoveRunnerRequest{Name: name})
		if err != nil {
			if ctx.Err() != nil {
				return err
			}

			failed++
			_, _ = fmt.Fprintf(errOut, "%s: %s\n", name, errorMessage(err))

			continue
		}

		if r.GetProvider() == "" {
			_, _ = fmt.Fprintf(out, "%s: registration removed from GitHub; it had no machine\n", name)
		} else {
			_, _ = fmt.Fprintf(out, "%s: removed from %s\n", name, r.GetProvider())
		}
	}

	if failed > 0 {
		return fmt.Errorf("%s not removed", plural(failed, "runner"))
	}

	return nil
}

// writeRunners writes a row per runner, indented, with their ages as at now,
// or says there are none.
func writeRunners(w io.Writer, p palette, runners []*rungarv1.Runner, now time.Time, indent int) {
	if len(runners) == 0 {
		_, _ = fmt.Fprintf(w, "%s%s\n", strings.Repeat(" ", indent), p.dim("none on the fleet right now"))

		return
	}

	t := newTable("RUNNER", "SCALE SET", "PROVIDER", "STATE", "MACHINE", "GITHUB", "AGE", "JOB").indented(indent)

	for _, r := range runners {
		t.addRow(r.GetName(), p.accent(r.GetScaleSet()), r.GetProvider(), p.status(runnerStateCell(r.GetState())),
			p.status(machineCell(r.GetMachineState())), p.status(gitHubStatusCell(r.GetGithubStatus())),
			ageSince(now, r.GetCreateTime()), jobCell(r.GetJob()))
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

// runnerStateCell returns a runner's STATE column.
func runnerStateCell(s rungarv1.RunnerState) string {
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

// gitHubStatusCell returns a runner's GITHUB column.
func gitHubStatusCell(s rungarv1.RunnerGitHubStatus) string {
	switch s {
	case rungarv1.RunnerGitHubStatus_RUNNER_GITHUB_STATUS_BUSY:
		return gitHubBusy
	case rungarv1.RunnerGitHubStatus_RUNNER_GITHUB_STATUS_IDLE:
		return gitHubIdle
	case rungarv1.RunnerGitHubStatus_RUNNER_GITHUB_STATUS_OFFLINE:
		return gitHubOffline
	case rungarv1.RunnerGitHubStatus_RUNNER_GITHUB_STATUS_NOT_REGISTERED:
		return gitHubUnregistered
	default:
		return "-"
	}
}

// jobCell returns a runner's JOB column: the job's repository and name, or
// "-" if the daemon does not know of one.
func jobCell(j *rungarv1.RunnerJob) string {
	name := cmp.Or(j.GetDisplayName(), j.GetId())
	switch {
	case name == "":
		return "-"
	case j.GetRepository() == "":
		return name
	default:
		return j.GetRepository() + ": " + name
	}
}

// machineCell returns a runner's MACHINE column: its machine's state.
func machineCell(state rungarv1.MachineState) string {
	switch state {
	case rungarv1.MachineState_MACHINE_STATE_STARTING:
		return machineStarting
	case rungarv1.MachineState_MACHINE_STATE_RUNNING:
		return machineRunning
	case rungarv1.MachineState_MACHINE_STATE_STOPPED:
		return machineStopped
	default:
		return "-"
	}
}
