// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

func newStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show what the daemon is doing: its scale sets, providers and runners",
		Long: "Show what the running daemon is doing: the GitHub it serves, its scale sets and how many " +
			"runners each has and wants, its providers as placement sees them (found full or failing " +
			"by a scale set, held for a scale set of higher priority, disabled), and every runner. " +
			"It is for reading: it exits non-zero only if the daemon cannot be asked.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withClient(cmd, func(client rungarv1.RungarServiceClient) error {
				return showStatus(cmd.Context(), cmd.OutOrStdout(), client)
			})
		},
	}
}

// showStatus writes the daemon's status report to out.
func showStatus(ctx context.Context, out io.Writer, client rungarv1.RungarServiceClient) error {
	st, err := client.GetStatus(ctx, &rungarv1.GetStatusRequest{})
	if err != nil {
		return err
	}

	writeStatus(out, paletteFor(out), st, time.Now())

	return nil
}

// writeStatus writes the status report. It is buffered and written at once, so
// a report is never cut short.
func writeStatus(w io.Writer, p palette, st *rungarv1.DaemonStatus, now time.Time) {
	var out bytes.Buffer

	d := st.GetDaemon()
	_, _ = fmt.Fprintf(&out, "%s %s\n\n", p.accent(p.bold("Rungar")), d.GetVersion())
	writeDaemon(&out, p, d, now)

	out.WriteString("\n")
	writeSection(&out, p, "Scale sets", len(st.GetScaleSets()))
	writeScaleSets(&out, p, st.GetScaleSets())

	out.WriteString("\n")
	writeSection(&out, p, "Providers", len(st.GetProviders()))
	writeProviders(&out, p, st.GetProviders())

	out.WriteString("\n")
	writeSection(&out, p, "Runners", len(st.GetRunners()))
	writeRunners(&out, p, st.GetRunners(), now, sectionIndent)
	// A GitHub error is shown beside the credentials instead.
	writeListingNotes(&out, p, sectionIndent, st.GetUnreachableProviders(), "")

	var allowed, running int
	for _, set := range st.GetScaleSets() {
		allowed += int(set.GetMaxRunners())
		running += runnerTotal(set.GetRunnerCounts())
	}
	_, _ = fmt.Fprintf(&out, "\n%s; the scale sets allow %d between them.\n",
		p.bold(plural(running, "runner")), allowed)

	_, _ = w.Write(out.Bytes())
}

// writeDaemon writes the GitHub the daemon serves, whether its credentials
// work, and how long it has been running.
func writeDaemon(w io.Writer, p palette, d *rungarv1.Daemon, now time.Time) {
	var f fields

	gh := d.GetGithub()
	f.add("GitHub", gh.GetUrl()+" "+p.dim("("+gh.GetScope()+")"))
	if err := gh.GetError(); err != "" {
		f.add("Credentials", gh.GetCredentials(), p.failure(err))
	} else {
		f.add("Credentials", gh.GetCredentials()+" "+p.status(statusOK))
	}
	f.add("Installation", d.GetInstallation())

	started := d.GetStartTime().AsTime()
	f.add("Running", "since "+started.Local().Format(time.DateTime)+" "+p.dim("("+age(now.Sub(started))+")"))
	f.add("Configuration", d.GetConfigFile())

	f.write(w, p)
}

// writeScaleSets writes a row per configured scale set, followed by a note for
// each one that is not listening.
func writeScaleSets(w io.Writer, p palette, sets []*rungarv1.ScaleSet) {
	t := newTable("SCALE SET", "STATUS", "PROVIDERS", "LABELS", "RUNNER", "RUNNERS", "DESIRED", "MIN", "MAX",
		"PRIORITY").indented(sectionIndent)

	var notes []string

	for _, set := range sets {
		labels := make([]string, 0, len(set.GetLabels()))
		for _, label := range set.GetLabels() {
			labels = append(labels, p.accent(label))
		}

		providers := strings.Join(set.GetProviders(), ",")
		if len(set.GetProviders()) > 1 {
			providers += " " + p.dim("("+set.GetPlacement()+")")
		}

		t.addRow(set.GetName(), p.status(scaleSetStatusCell(set)), providers, strings.Join(labels, ","), runnerSizesCell(set),
			runnerCountCell(p, set.GetRunnerCounts()), strconv.Itoa(int(set.GetDesiredRunners())),
			strconv.Itoa(int(set.GetMinRunners())), strconv.Itoa(int(set.GetMaxRunners())),
			strconv.Itoa(int(set.GetPriority())))

		if note := scaleSetStatusNote(set); note != "" {
			notes = append(notes, set.GetName()+": "+note)
		}
	}

	t.write(w, p)

	for _, note := range notes {
		_, _ = fmt.Fprintf(w, "%s%s\n", strings.Repeat(" ", sectionIndent), p.dim(note))
	}
}

// writeProviders writes a row per provider, followed by why any provider is
// unreachable or skipped.
func writeProviders(w io.Writer, p palette, providers []*rungarv1.Provider) {
	t := newTable("PROVIDER", "TYPE", "STATUS", "RUNNERS").indented(sectionIndent)

	for _, pr := range providers {
		t.addRow(pr.GetName(), pr.GetType(), providerStatusCell(p, pr), providerRunnersCell(pr))
	}

	t.write(w, p)
	writeProviderErrors(w, p, sectionIndent, providers)
	writeSkipNotes(w, p, sectionIndent, providers)
}
