// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

func newScaleSetsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "scale-sets",
		Aliases: []string{"scale-set"},
		Short:   "List, inspect, pause, resume and remove scale sets",
		Long: "List, inspect and remove scale sets: what the configuration, the daemon, GitHub and the " +
			"fleet each have of them. Pause or resume one while the daemon runs.",
		Args: cobra.NoArgs,
	}

	cmd.AddCommand(newScaleSetListCommand(), newScaleSetInspectCommand(), newScaleSetPauseCommand(),
		newScaleSetResumeCommand(), newScaleSetRemoveCommand())

	return cmd
}

func newScaleSetListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List the configured scale sets, and those found only by their runners",
		Long: "List the configured scale sets, and those no longer configured whose runners are still on " +
			"the fleet. GitHub cannot be asked for every scale set it has, so one that is neither " +
			"configured nor has a runner is not listed; inspect and rm still find it by name.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			asJSON, _ := cmd.Flags().GetBool("json")

			return withClient(cmd, func(client rungarv1.RungarServiceClient) error {
				return listScaleSets(cmd.Context(), cmd.OutOrStdout(), client, asJSON)
			})
		},
	}
	cmd.Flags().Bool("json", false, "Write JSON")

	return cmd
}

func newScaleSetInspectCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect NAME",
		Short: "Show one scale set: in the configuration, in the daemon, on GitHub, and its runners",
		Long: "Show one scale set: what the configuration says, what the daemon is doing with it, what " +
			"GitHub has of it, and its runners on the fleet. A configured one ends with the runs-on " +
			"line that targets it, for a workflow to copy.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			asJSON, _ := cmd.Flags().GetBool("json")
			group, _ := cmd.Flags().GetString("group")

			return withClient(cmd, func(client rungarv1.RungarServiceClient) error {
				return inspectScaleSet(cmd.Context(), cmd.OutOrStdout(), client, args[0], group, asJSON)
			})
		},
	}
	cmd.Flags().Bool("json", false, "Write JSON")
	cmd.Flags().String("group", "", "Runner group to look for it in on GitHub (default the configured one, or Default)")

	return cmd
}

func newScaleSetRemoveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "rm NAME",
		Aliases: []string{"remove", "delete"},
		Short:   "Remove a scale set that is no longer configured: its runners, then the scale set on GitHub",
		Long: "Remove a scale set for good: its runners, the safe way round (each runner's registration " +
			"first, which GitHub refuses for one running a job), and then the scale set on GitHub. A " +
			"runner running a job is left to finish, and the scale set is kept until it has, unless " +
			"--wait says to wait for it. A scale set still in the configuration is refused: the daemon " +
			"would create it again.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			group, _ := cmd.Flags().GetString("group")
			wait, _ := cmd.Flags().GetBool("wait")

			return withClient(cmd, func(client rungarv1.RungarServiceClient) error {
				return removeScaleSet(cmd.Context(), cmd.OutOrStdout(), client, args[0], group, wait)
			})
		},
	}
	cmd.Flags().String("group", types.DefaultRunnerGroup, "Runner group the scale set is in on GitHub")
	cmd.Flags().Bool("wait", false, "Wait for runners running jobs to finish, then remove the scale set")

	return cmd
}

// listScaleSets writes the configured scale sets, and the leftovers.
func listScaleSets(ctx context.Context, out io.Writer, client rungarv1.RungarServiceClient, asJSON bool) error {
	resp, err := client.ListScaleSets(ctx, &rungarv1.ListScaleSetsRequest{})
	if err != nil {
		return err
	}

	if asJSON {
		return writeJSON(out, resp)
	}

	p := paletteFor(out)
	t := newTable("NAME", "STATUS", "ID", "GROUP", "RUNNERS", "ASSIGNED", "RUNNING", "IDLE")

	var notes []string

	for _, set := range resp.GetScaleSets() {
		status := p.status(scaleSetLeftover)
		if set.GetConfigured() {
			status = p.status(scaleSetStatusCell(set))
		}

		gh := set.GetGithub()
		id, group := "-", "-"
		if gh.GetFound() {
			id, group = strconv.FormatInt(gh.GetId(), 10), gh.GetRunnerGroup()
		}

		assigned, running, idle := "-", "-", "-"
		if s := gh.GetStatistics(); s != nil {
			assigned = strconv.Itoa(int(s.GetAssignedJobs()))
			running = strconv.Itoa(int(s.GetRunningJobs()))
			idle = strconv.Itoa(int(s.GetIdleRunners()))
		}

		t.addRow(p.accent(set.GetName()), status, id, group, strconv.Itoa(int(set.GetFleetRunners())),
			assigned, running, idle)

		if gh.GetError() != "" {
			notes = append(notes, set.GetName()+": "+gh.GetError())
		}
	}

	t.write(out, p)

	for _, note := range notes {
		_, _ = fmt.Fprintln(out, p.dim("GitHub could not be asked about "+note))
	}
	writeListingNotes(out, p, 0, resp.GetUnreachableProviders(), "")

	return nil
}

// inspectScaleSet writes one scale set and its runners.
func inspectScaleSet(ctx context.Context, out io.Writer, client rungarv1.RungarServiceClient,
	name, group string, asJSON bool,
) error {
	set, err := client.GetScaleSet(ctx, &rungarv1.GetScaleSetRequest{Name: name, RunnerGroup: group})
	if err != nil {
		return err
	}

	runners, err := client.ListRunners(ctx, &rungarv1.ListRunnersRequest{ScaleSet: name})
	if err != nil {
		return err
	}

	if asJSON {
		return writeJSONObject(out, map[string]proto.Message{"scale_set": set, "runners": runners})
	}

	p := paletteFor(out)
	writeScaleSet(out, p, set)

	_, _ = fmt.Fprintln(out)
	writeSection(out, p, "Runners", len(runners.GetRunners()))
	writeRunners(out, p, runners.GetRunners(), time.Now(), sectionIndent)
	writeListingNotes(out, p, sectionIndent, runners.GetUnreachableProviders(), runners.GetGithubError())

	return nil
}

// scheduleLines describes a scale set's schedule, a line each: its time zone,
// its windows in order with the one in force marked, and the min_runners
// outside them.
func scheduleLines(p palette, set *rungarv1.ScaleSet) []string {
	schedule := set.GetSchedule()
	lines := make([]string, 0, len(schedule.GetWindows())+2)
	lines = append(lines, schedule.GetTimeZone())

	for _, w := range schedule.GetWindows() {
		line := fmt.Sprintf("%s %s-%s, at least %d", w.GetDays(), w.GetFrom(), w.GetTo(), w.GetMinRunners())
		if w.GetInForce() {
			line += " " + p.dim("(in force)")
		}

		lines = append(lines, line)
	}

	other := fmt.Sprintf("otherwise, at least %d", set.GetConfiguredMinRunners())
	if !slices.ContainsFunc(schedule.GetWindows(), (*rungarv1.ScheduleWindow).GetInForce) {
		other += " " + p.dim("(in force)")
	}

	return append(lines, other)
}

// writeScaleSet writes one scale set's fields: its configuration and state if
// configured, then what GitHub has of it.
func writeScaleSet(w io.Writer, p palette, set *rungarv1.ScaleSet) {
	var f fields

	f.add("Name", p.accent(set.GetName()))

	if set.GetConfigured() {
		status := p.status(scaleSetStatusCell(set))
		if note := scaleSetStatusNote(set); note != "" {
			status += " " + p.dim(note)
		}

		f.add("Status", status)
		f.add("Providers", strings.Join(set.GetProviders(), ", ")+" "+p.dim("("+set.GetPlacement()+")"))
		f.add("Runner", runnerSizesCell(set))
		f.add("Runners", fmt.Sprintf("%s, %d desired, at least %d, at most %d",
			runnerCountCell(p, set.GetRunnerCounts()), set.GetDesiredRunners(), set.GetMinRunners(), set.GetMaxRunners()))
		if schedule := set.GetSchedule(); schedule != nil {
			f.add("Schedule", scheduleLines(p, set)...)
		}
		if set.GetPriority() != 0 {
			f.add("Priority", strconv.Itoa(int(set.GetPriority())))
		}
	} else {
		f.add("Status", p.status(scaleSetLeftover)+" "+p.dim("no longer configured; its runners are not looked after"))
		f.add("Runners", strconv.Itoa(int(set.GetFleetRunners())))
	}

	switch gh := set.GetGithub(); {
	case gh.GetError() != "":
		f.add("GitHub", p.dim("could not be asked: "+gh.GetError()))
	case !gh.GetFound():
		f.add("GitHub", p.dim("no scale set of this name"))
	default:
		f.add("ID", strconv.FormatInt(gh.GetId(), 10))
		f.add("Group", gh.GetRunnerGroup())
		f.add("Labels", strings.Join(gh.GetLabels(), ", "))
		if gh.GetCreateTime() != nil {
			f.add("Created", gh.GetCreateTime().AsTime().Format(time.RFC3339))
		}
		if s := gh.GetStatistics(); s != nil {
			f.add("Jobs", fmt.Sprintf("%d assigned, %d running", s.GetAssignedJobs(), s.GetRunningJobs()))
			f.add("On GitHub", fmt.Sprintf("%s registered, %d busy, %d idle",
				plural(int(s.GetRegisteredRunners()), "runner"), s.GetBusyRunners(), s.GetIdleRunners()))
		}
	}

	// Last, as the line to copy into a workflow.
	if set.GetRunsOn() != "" {
		f.add("Workflows", set.GetRunsOn())
	}

	f.write(w, p)
}

// removeScaleSet has the daemon remove a scale set, writing each runner it
// removes. While runners are running jobs, it fails, or with wait polls until
// they have finished.
func removeScaleSet(ctx context.Context, out io.Writer, client rungarv1.RungarServiceClient,
	name, group string, wait bool,
) error {
	for {
		resp, err := client.RemoveScaleSet(ctx, &rungarv1.RemoveScaleSetRequest{Name: name, RunnerGroup: group})
		if err != nil {
			return err
		}

		for _, r := range resp.GetRemoved() {
			_, _ = fmt.Fprintf(out, "Removed runner %s from %s\n", r.GetName(), r.GetProvider())
		}

		busy := int(resp.GetBusyLeft())
		switch {
		case busy == 0 && resp.GetScaleSetId() != 0:
			_, _ = fmt.Fprintf(out, "Removed scale set %q (%d) from GitHub\n", name, resp.GetScaleSetId())
			return nil
		case busy == 0:
			_, _ = fmt.Fprintf(out, "GitHub has no scale set %q in runner group %q\n", name, group)
			return nil
		case !wait:
			return errdefs.Busy("scale set %q has runners running jobs (%d), which are left to finish; "+
				"run this again once they have, or with --wait", name, busy)
		}

		jobs := "their jobs"
		if busy == 1 {
			jobs = "its job"
		}
		_, _ = fmt.Fprintf(out, "Waiting for %s to finish %s\n", plural(busy, "runner"), jobs)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// scaleSetStatusCell returns a configured scale set's STATUS column. Paused
// comes first: it is someone's decision, which the rest follow from.
func scaleSetStatusCell(set *rungarv1.ScaleSet) string {
	switch {
	case set.GetPaused():
		return scaleSetPaused
	case set.GetHoldingBackReason() != "":
		return scaleSetHoldingBack
	case set.GetPhase() == rungarv1.ScaleSetPhase_SCALE_SET_PHASE_LISTENING:
		return scaleSetListening
	case set.GetPhase() == rungarv1.ScaleSetPhase_SCALE_SET_PHASE_WAITING_FOR_SESSION,
		set.GetPhase() == rungarv1.ScaleSetPhase_SCALE_SET_PHASE_WAITING_FOR_LEAD:
		return scaleSetWaiting
	case set.GetPhase() == rungarv1.ScaleSetPhase_SCALE_SET_PHASE_GITHUB_UNREACHABLE:
		return statusUnreachable
	default:
		return scaleSetStarting
	}
}

// scaleSetStatusNote explains a scale set's status, or returns "" if it is
// listening as the configuration has it.
func scaleSetStatusNote(set *rungarv1.ScaleSet) string {
	switch scaleSetStatusCell(set) {
	case scaleSetPaused:
		note := "taking no jobs, which wait on GitHub"
		if n := runnerTotal(set.GetRunnerCounts()); n > 0 {
			note += "; " + plural(n, "runner") + " left"
		}
		if !set.GetConfiguredPaused() {
			note += "; until resumed or the daemon restarts"
		}

		return note
	case scaleSetHoldingBack:
		return set.GetHoldingBackReason()
	case scaleSetWaiting:
		if set.GetPhase() == rungarv1.ScaleSetPhase_SCALE_SET_PHASE_WAITING_FOR_LEAD {
			return "standing by until this daemon holds the lead scale set's message session"
		}

		return "another message session holds the scale set; standing by until it ends"
	case statusUnreachable:
		return "GitHub does not answer; trying again for the message session"
	case scaleSetStarting:
		return "being looked up on GitHub, and its runners adopted"
	case scaleSetListening:
		if set.GetConfiguredPaused() {
			return "resumed until the daemon restarts; the configuration has it paused"
		}

		return ""
	default:
		return ""
	}
}

// runnerSizesCell returns each distinct size a scale set's providers describe
// its runner as, or "-" if none does.
func runnerSizesCell(set *rungarv1.ScaleSet) string {
	var sizes []string
	for _, size := range set.GetRunnerSizes() {
		if s := size.GetDescription(); s != "" && !slices.Contains(sizes, s) {
			sizes = append(sizes, s)
		}
	}

	if len(sizes) == 0 {
		return "-"
	}

	return strings.Join(sizes, "; ")
}

// runnerCountCell returns how many runners a scale set has, and how many are
// busy or starting: "3 (1 busy, 1 starting)".
func runnerCountCell(p palette, counts *rungarv1.RunnerCounts) string {
	total := runnerTotal(counts)
	if total == 0 {
		return "0"
	}

	var parts []string
	if n := counts.GetBusy(); n > 0 {
		parts = append(parts, fmt.Sprintf("%d busy", n))
	}
	if n := counts.GetStarting(); n > 0 {
		parts = append(parts, fmt.Sprintf("%d starting", n))
	}

	if len(parts) == 0 {
		return strconv.Itoa(total)
	}

	return strconv.Itoa(total) + " " + p.dim("("+strings.Join(parts, ", ")+")")
}

// runnerTotal returns how many runners a scale set has, in any state.
func runnerTotal(counts *rungarv1.RunnerCounts) int {
	return int(counts.GetStarting() + counts.GetIdle() + counts.GetBusy())
}
