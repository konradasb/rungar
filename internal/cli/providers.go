// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

func newProvidersCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "providers",
		Aliases: []string{"provider"},
		Short:   "List, inspect, disable, drain and enable providers",
		Long: "List and inspect providers: whether each can be reached, which scale sets are skipping " +
			"it for being full or refusing to create their runners, and what of Rungar's is on it. " +
			"Disable, drain or enable one while the daemon runs.",
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
		Short:   "List providers: their status, runners, weight and scale sets",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			asJSON, _ := cmd.Flags().GetBool("json")

			return withClient(cmd, func(client rungarv1.RungarServiceClient) error {
				return listProviders(cmd.Context(), cmd.OutOrStdout(), client, asJSON)
			})
		},
	}
	cmd.Flags().Bool("json", false, "Write JSON")

	return cmd
}

func newProviderInspectCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect NAME",
		Short: "Show one provider: its status, the scale sets on it, and its runners",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			asJSON, _ := cmd.Flags().GetBool("json")

			return withClient(cmd, func(client rungarv1.RungarServiceClient) error {
				return inspectProvider(cmd.Context(), cmd.OutOrStdout(), client, args[0], asJSON)
			})
		},
	}
	cmd.Flags().Bool("json", false, "Write JSON")

	return cmd
}

// listProviders writes every provider, and why any is skipped.
func listProviders(ctx context.Context, out io.Writer, client rungarv1.RungarServiceClient, asJSON bool) error {
	resp, err := client.ListProviders(ctx, &rungarv1.ListProvidersRequest{})
	if err != nil {
		return err
	}

	if asJSON {
		return writeJSON(out, resp)
	}

	p := paletteFor(out)
	t := newTable("NAME", "TYPE", "STATUS", "RUNNERS", "WEIGHT", "SCALE SETS")

	for _, pr := range resp.GetProviders() {
		names := make([]string, 0, len(pr.GetScaleSets()))
		for _, set := range pr.GetScaleSets() {
			names = append(names, set.GetScaleSet())
		}
		scaleSets := strings.Join(names, ",")
		if scaleSets == "" {
			scaleSets = p.dim("none")
		}

		t.addRow(pr.GetName(), pr.GetType(), providerStatusCell(p, pr), providerRunnersCell(pr),
			strconv.FormatFloat(pr.GetWeight(), 'g', -1, 64), scaleSets)
	}

	t.write(out, p)
	writeProviderErrors(out, p, 0, resp.GetProviders())
	writeSkipNotes(out, p, 0, resp.GetProviders())

	return nil
}

// inspectProvider writes one provider and its runners.
func inspectProvider(ctx context.Context, out io.Writer, client rungarv1.RungarServiceClient,
	name string, asJSON bool,
) error {
	pr, err := client.GetProvider(ctx, &rungarv1.GetProviderRequest{Name: name})
	if err != nil {
		return err
	}

	runners, err := client.ListRunners(ctx, &rungarv1.ListRunnersRequest{Provider: name})
	if err != nil {
		return err
	}

	if asJSON {
		return writeJSONObject(out, map[string]proto.Message{"provider": pr, "runners": runners})
	}

	p := paletteFor(out)
	writeProvider(out, p, pr)

	_, _ = fmt.Fprintln(out)
	writeSection(out, p, "Runners", len(runners.GetRunners()))
	writeRunners(out, p, runners.GetRunners(), time.Now(), sectionIndent)
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
	f.add("Status", providerStatusCell(p, pr))
	if !pr.GetReachable() && pr.GetError() != "" {
		f.add("Error", pr.GetError())
	}
	f.add("Runners", providerRunnersCell(pr))
	f.add("Weight", strconv.FormatFloat(pr.GetWeight(), 'g', -1, 64))

	lines := make([]string, 0, len(pr.GetScaleSets()))
	for _, set := range pr.GetScaleSets() {
		line := p.accent(set.GetScaleSet())
		if note := skipNote(set); note != "" {
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

// providerStatusCell returns a provider's STATUS column: the main thing keeping
// runners off it, if any. Why it is unreachable is written under the table;
// see writeProviderErrors.
func providerStatusCell(p palette, pr *rungarv1.Provider) string {
	switch {
	case !pr.GetReachable():
		return p.status(statusUnreachable)
	case pr.GetDisabled():
		status := p.status(providerDisabled)
		if n := int(pr.GetRunnerCount()); n > 0 {
			status = p.status(providerDraining) + " " + p.dim(plural(n, "runner")+" left")
		}
		if !pr.GetConfiguredDisabled() {
			status += " " + p.dim("until the daemon restarts")
		}

		return status
	case pr.GetHold() != nil:
		h := pr.GetHold()
		return p.status(providerHeld) + " " + p.dim(fmt.Sprintf("for %s (priority %d), %s left",
			h.GetScaleSet(), h.GetPriority(), h.GetRemaining().AsDuration().Round(time.Second)))
	case pr.GetConfiguredDisabled():
		return p.status(statusOK) + " " + p.dim("enabled until the daemon restarts")
	default:
		return p.status(statusOK)
	}
}

// providerRunnersCell returns a provider's RUNNERS column: its runners,
// against its limit if it has one: "6/8".
func providerRunnersCell(pr *rungarv1.Provider) string {
	if pr.GetMaxRunners() > 0 {
		return fmt.Sprintf("%d/%d", pr.GetRunnerCount(), pr.GetMaxRunners())
	}

	return strconv.Itoa(int(pr.GetRunnerCount()))
}

// skipNote returns why a scale set skips the provider, or "" if it does
// not: "full, tried again in 9s".
func skipNote(set *rungarv1.ProviderScaleSet) string {
	state, detail, skipped := skipReason(set)
	if !skipped {
		return ""
	}

	return state + ", tried again in " + roundedBackoff(set) + detail
}

// skipReason says why a scale set skips the provider: state is what the
// scale set found it, "full" or "failing (2 in a row)", and detail is ": " and
// the last refusal, if there was one. skipped is false if it does not skip it.
func skipReason(set *rungarv1.ProviderScaleSet) (state, detail string, skipped bool) {
	switch {
	case set.GetBackoffFor() == nil:
		return "", "", false
	case set.GetFull():
		return "full", "", true
	default:
		return fmt.Sprintf("failing (%d in a row)", set.GetRefusals()), ": " + set.GetRefusal(), true
	}
}

// roundedBackoff returns how long the scale set still skips the provider, to
// the second.
func roundedBackoff(set *rungarv1.ProviderScaleSet) string {
	return set.GetBackoffFor().AsDuration().Round(time.Second).String()
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

// writeSkipNotes writes, indented, each scale set that skips a provider
// and why.
func writeSkipNotes(w io.Writer, p palette, indent int, providers []*rungarv1.Provider) {
	prefix := strings.Repeat(" ", indent)

	for _, pr := range providers {
		for _, set := range pr.GetScaleSets() {
			state, detail, skipped := skipReason(set)
			if !skipped {
				continue
			}

			note := fmt.Sprintf("%s found %s %s; tries it again in %s%s",
				set.GetScaleSet(), pr.GetName(), state, roundedBackoff(set), detail)
			_, _ = fmt.Fprintf(w, "%s%s\n", prefix, p.dim(note))
		}
	}
}
