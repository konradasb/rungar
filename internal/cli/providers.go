// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// Words of a provider's STATUS column. statusOK also marks working
// credentials.
const (
	statusOK          = "OK"
	statusDisabled    = "DISABLED"
	statusDraining    = "DRAINING"
	statusHeld        = "HELD"
	statusUnreachable = "UNREACHABLE"
)

func newProvidersCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "providers",
		Aliases: []string{"provider"},
		Short:   "List, inspect, disable and enable providers",
		Long: "List and inspect providers: whether each can be reached, which scale sets are skipping " +
			"it for finding it full or failing to make their runners, what it says it has spare, and " +
			"what of Rungar's is on it. Disable, drain or enable one while the daemon runs.",
		Args: cobra.NoArgs,
	}

	cmd.AddCommand(newProviderListCommand(), newProviderInspectCommand(),
		newProviderDisableCommand(), newProviderDrainCommand(), newProviderEnableCommand())

	return cmd
}

func newProviderListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List providers: their status, room, runners and scale sets",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			asJSON, _ := cmd.Flags().GetBool("json")

			return withClient(cmd, func(client rungarv1.RungarServiceClient) error {
				return listProviders(cmd, client, asJSON)
			})
		},
	}
	cmd.Flags().Bool("json", false, "Write JSON")

	return cmd
}

func newProviderInspectCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect NAME",
		Short: "Show one provider: its status and room, the scale sets on it, and its runners",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			asJSON, _ := cmd.Flags().GetBool("json")

			return withClient(cmd, func(client rungarv1.RungarServiceClient) error {
				return inspectProvider(cmd, client, args[0], asJSON)
			})
		},
	}
	cmd.Flags().Bool("json", false, "Write JSON")

	return cmd
}

func listProviders(cmd *cobra.Command, client rungarv1.RungarServiceClient, asJSON bool) error {
	resp, err := client.ListProviders(cmd.Context(), &rungarv1.ListProvidersRequest{})
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if asJSON {
		return writeJSON(out, resp)
	}

	p := paletteFor(out)
	t := newTable("NAME", "TYPE", "STATUS", "RUNNERS", "WEIGHT", "SCALE SETS")

	for _, pr := range resp.GetProviders() {
		scaleSets := strings.Join(pr.GetScaleSets(), ",")
		if scaleSets == "" {
			scaleSets = p.dim("none")
		}

		t.row(pr.GetName(), pr.GetType(), providerStatus(p, pr), runnersOf(pr),
			strconv.FormatFloat(pr.GetWeight(), 'g', -1, 64), scaleSets)
	}

	t.write(out, p)
	writeProviderErrors(out, p, 0, resp.GetProviders())
	writePlacementNotes(out, p, 0, resp.GetProviders())

	return nil
}

func inspectProvider(cmd *cobra.Command, client rungarv1.RungarServiceClient, name string, asJSON bool) error {
	ctx := cmd.Context()

	pr, err := client.GetProvider(ctx, &rungarv1.GetProviderRequest{Name: name})
	if err != nil {
		return err
	}

	runners, err := client.ListRunners(ctx, &rungarv1.ListRunnersRequest{Provider: name})
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if asJSON {
		return writeJSONObject(out, map[string]proto.Message{"provider": pr, "runners": runners})
	}

	p := paletteFor(out)
	writeProvider(out, p, pr)

	_, _ = fmt.Fprintln(out)
	writeSection(out, p, "Runners", len(runners.GetRunners()))
	writeRunners(out, p, runners.GetRunners(), sectionIndent)
	writeListingNotes(out, p, sectionIndent, runners.GetUnreachableProviders(), runners.GetGithubError())

	return nil
}

// writeProvider writes one provider's fields, with how it stands for each
// scale set placed on it.
func writeProvider(w io.Writer, p palette, pr *rungarv1.Provider) {
	var f fields

	f.add("Name", pr.GetName())
	f.add("Type", pr.GetType())
	if pr.GetEndpoint() != "" {
		f.add("Endpoint", pr.GetEndpoint())
	}
	f.add("Status", providerStatus(p, pr))
	if !pr.GetReachable() && pr.GetError() != "" {
		f.add("Error", pr.GetError())
	}
	f.add("Runners", runnersOf(pr))
	f.add("Weight", strconv.FormatFloat(pr.GetWeight(), 'g', -1, 64))

	lines := make([]string, 0, len(pr.GetPlacements()))
	for _, pl := range pr.GetPlacements() {
		line := p.accent(pl.GetScaleSet())
		if note := placementNote(pl); note != "" {
			line += " " + p.dim(note)
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = append(lines, p.dim("none is placed on it"))
	}
	f.add("Scale sets", lines...)

	f.write(w, p)
}

// providerStatus returns a provider's STATUS column: the main thing keeping
// runners off it, if any. Why it is unreachable is written under the table;
// see writeProviderErrors.
func providerStatus(p palette, pr *rungarv1.Provider) string {
	switch {
	case !pr.GetReachable():
		return p.status(statusUnreachable)
	case pr.GetDisabled():
		status := p.status(statusDisabled)
		if n := int(pr.GetRunnerCount()); n > 0 {
			status = p.status(statusDraining) + " " + p.dim(plural(n, "runner")+" left")
		}
		if !pr.GetConfiguredDisabled() {
			status += " " + p.dim("until the daemon restarts")
		}

		return status
	case pr.GetHold() != nil:
		h := pr.GetHold()
		return p.status(statusHeld) + " " + p.dim(fmt.Sprintf("for %s (priority %d), %s left",
			h.GetScaleSet(), h.GetPriority(), h.GetRemaining().AsDuration().Round(time.Second)))
	case pr.GetConfiguredDisabled():
		return p.status(statusOK) + " " + p.dim("enabled until the daemon restarts")
	default:
		return p.status(statusOK)
	}
}

// runnersOf returns a provider's RUNNERS column: its runners, against its
// limit if it has one: "6/8".
func runnersOf(pr *rungarv1.Provider) string {
	if pr.GetMaxRunners() > 0 {
		return fmt.Sprintf("%d/%d", pr.GetRunnerCount(), pr.GetMaxRunners())
	}

	return strconv.Itoa(int(pr.GetRunnerCount()))
}

// placementNote returns why a scale set skips the provider, or "" if it does
// not.
func placementNote(pl *rungarv1.ProviderScaleSet) string {
	switch {
	case pl.GetBackoffFor() == nil:
		return ""
	case pl.GetFull():
		return "full, tried again in " + roundedBackoff(pl)
	default:
		return fmt.Sprintf("failing (%d in a row), tried again in %s: %s",
			pl.GetFailures(), roundedBackoff(pl), pl.GetFailure())
	}
}

// roundedBackoff returns how long the scale set still skips the provider, to
// the second.
func roundedBackoff(pl *rungarv1.ProviderScaleSet) string {
	return pl.GetBackoffFor().AsDuration().Round(time.Second).String()
}

// writeProviderErrors writes, indented, why each unreachable provider could not
// be reached.
func writeProviderErrors(w io.Writer, p palette, indent int, providers []*rungarv1.Provider) {
	prefix := strings.Repeat(" ", indent)

	for _, pr := range providers {
		if !pr.GetReachable() && pr.GetError() != "" {
			_, _ = fmt.Fprintf(w, "%s%s\n", prefix,
				p.dim(fmt.Sprintf("%s cannot be reached: %s", pr.GetName(), pr.GetError())))
		}
	}
}

// writePlacementNotes writes, indented, each scale set that skips a provider
// and why.
func writePlacementNotes(w io.Writer, p palette, indent int, providers []*rungarv1.Provider) {
	prefix := strings.Repeat(" ", indent)

	for _, pr := range providers {
		for _, pl := range pr.GetPlacements() {
			var note string
			switch {
			case pl.GetBackoffFor() == nil:
				continue
			case pl.GetFull():
				note = fmt.Sprintf("%s found %s full; tries it again in %s",
					pl.GetScaleSet(), pr.GetName(), roundedBackoff(pl))
			default:
				note = fmt.Sprintf("%s found %s failing (%d in a row); tries it again in %s: %s",
					pl.GetScaleSet(), pr.GetName(), pl.GetFailures(), roundedBackoff(pl), pl.GetFailure())
			}
			_, _ = fmt.Fprintf(w, "%s%s\n", prefix, p.dim(note))
		}
	}
}
