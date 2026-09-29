// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	// sectionIndent is how far a section's contents are indented under its
	// heading.
	sectionIndent = 2

	// gutter is the space between two columns.
	gutter = 3
)

// table lays out rows in aligned columns. Unlike text/tabwriter, it measures
// cells by their visible width, so colour does not misalign them.
type table struct {
	headers []string
	rows    [][]string
	indent  int
}

func newTable(headers ...string) *table {
	return &table{headers: headers}
}

func (t *table) addRow(cells ...string) {
	t.rows = append(t.rows, cells)
}

// indented indents the table by n spaces.
func (t *table) indented(n int) *table {
	t.indent = n

	return t
}

// write renders the table with bold headers. The last column is not padded,
// so no line has trailing spaces.
func (t *table) write(w io.Writer, p palette) {
	widths := make([]int, len(t.headers))
	for i, h := range t.headers {
		widths[i] = visibleWidth(h)
	}
	for _, row := range t.rows {
		for i, cell := range row {
			if i < len(widths) {
				widths[i] = max(widths[i], visibleWidth(cell))
			}
		}
	}

	var b strings.Builder

	prefix := strings.Repeat(" ", t.indent)

	headers := make([]string, len(t.headers))
	for i, h := range t.headers {
		headers[i] = p.bold(h)
	}
	writeRow(&b, prefix, headers, widths)

	for _, row := range t.rows {
		writeRow(&b, prefix, row, widths)
	}

	_, _ = io.WriteString(w, b.String())
}

func writeRow(b *strings.Builder, prefix string, cells []string, widths []int) {
	b.WriteString(prefix)

	for i, cell := range cells {
		b.WriteString(cell)
		if i < len(cells)-1 {
			b.WriteString(strings.Repeat(" ", max(widths[i]-visibleWidth(cell)+gutter, 1)))
		}
	}

	b.WriteString("\n")
}

// pad pads s with spaces to n visible columns.
func pad(s string, n int) string {
	return s + strings.Repeat(" ", max(n-visibleWidth(s), 0))
}

// writeSection writes a section heading with the number of items under it.
func writeSection(w io.Writer, p palette, title string, n int) {
	_, _ = fmt.Fprintf(w, "%s %s\n", p.bold(title), p.dim("("+strconv.Itoa(n)+")"))
}

// fields lays out labelled values with right-aligned labels:
//
//	     GitHub: https://github.com/my-org (organisation)
//	Credentials: GitHub personal access token (source: /etc/rungar/token)
type fields struct {
	labels []string
	values [][]string
}

// add adds a field. A field of several values has each on its own line.
func (f *fields) add(label string, values ...string) {
	f.labels = append(f.labels, label)
	f.values = append(f.values, values)
}

func (f *fields) write(w io.Writer, p palette) {
	width := 0
	for _, label := range f.labels {
		width = max(width, visibleWidth(label))
	}

	var b strings.Builder

	for i, label := range f.labels {
		for j, value := range f.values[i] {
			if j == 0 {
				b.WriteString(strings.Repeat(" ", width-visibleWidth(label)) + p.dim(label+":") + " " + value + "\n")
				continue
			}
			b.WriteString(strings.Repeat(" ", width+2) + value + "\n")
		}
	}

	_, _ = io.WriteString(w, b.String())
}
