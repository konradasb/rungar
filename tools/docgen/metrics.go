// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/konradasb/rungar/internal/metrics"
)

// metricSection is a section of the metrics reference: a group's table, and
// what follows it.
type metricSection struct {
	group, heading, after string
}

// metricSections are the reference's sections, in order. A metric in a group
// with no section fails the generation.
var metricSections = []metricSection{
	{
		group:   metrics.GroupDaemon,
		heading: "Daemon",
	},
	{
		group:   metrics.GroupGitHub,
		heading: "GitHub",
		after: "An expired or revoked credential shows as a rate of `401`s, one without a permission " +
			"as `403`s or `404`s, and an outage as `5xx`s and `error`s -- before, or instead of, the " +
			"daemon restarting. See [Monitoring]({{< relref \"/docs/guides/monitoring\" >}}) for alerts on them.",
	},
	{
		group:   metrics.GroupScaleSets,
		heading: "Scale sets",
		after: "The gauges are updated on every reconciliation -- every `reconcile_interval`, 30 seconds " +
			"unless set -- and the counters as things happen. The runners counted are those of the " +
			"scale set Rungar knows of: its own, and those it has adopted.",
	},
	{
		group:   metrics.GroupProviders,
		heading: "Providers",
		after: "It is updated every `reconcile_interval`, from what the daemon has learnt of each " +
			"provider by using it: whether it answered the last call made of it.",
	},
}

// metricsIntro opens the page.
const metricsIntro = "The Prometheus metrics the daemon serves at `/metrics`, once " +
	"[enabled]({{< relref \"/docs/reference/configuration#metrics\" >}}) with `metrics.enable`. " +
	"Every metric of Rungar's own is named `rungar_`. See " +
	"[Monitoring]({{< relref \"/docs/guides/monitoring\" >}}) for scraping and alerts.\n\n" +
	"The endpoint also serves the Go runtime's `go_*` metrics and the daemon process's `process_*` " +
	"metrics.\n"

// writeMetrics writes the metrics reference from those the daemon registers.
func writeMetrics(dir string) error {
	byGroup := map[string][]metrics.Description{}
	for _, m := range metrics.New().Reference() {
		byGroup[m.Group] = append(byGroup[m.Group], m)
	}

	var body bytes.Buffer
	body.WriteString(metricsIntro)

	for _, sec := range metricSections {
		fmt.Fprintf(&body, "\n## %s\n\n", sec.heading)
		body.WriteString("| Metric | Type | Labels | Description |\n|---|---|---|---|\n")
		for _, m := range byGroup[sec.group] {
			labels := make([]string, len(m.Labels))
			for i, l := range m.Labels {
				labels[i] = "`" + l + "`"
			}

			text := m.Help
			if m.Doc != "" {
				text += " " + m.Doc
			}

			fmt.Fprintf(&body, "| `%s` | %s | %s | %s |\n", m.Name, m.Type, strings.Join(labels, ", "), text)
		}

		if sec.after != "" {
			body.WriteString("\n" + sec.after + "\n")
		}

		delete(byGroup, sec.group)
	}

	for group, ms := range byGroup {
		return fmt.Errorf("%s is in group %q, which the reference has no section for", ms[0].Name, group)
	}

	return writePage(filepath.Join(dir, "metrics.md"), meta{
		title: "Metrics", weight: 4, icon: "chart-bar",
		description: "Every metric Rungar serves, with its labels.",
	}, body.Bytes())
}
