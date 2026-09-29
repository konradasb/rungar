// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestTableAlignsColouredCells checks colour does not misalign columns, which
// is why table is used rather than text/tabwriter.
func TestTableAlignsColouredCells(t *testing.T) {
	p := palette{enabled: true}

	tbl := newTable("PROVIDER", "STATUS", "RUNNERS")
	tbl.addRow("compute1", p.status(statusOK), "4 runners")
	tbl.addRow("compute2", p.status(statusUnreachable), "-")

	var buf bytes.Buffer
	tbl.write(&buf, p)

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("wrote %d lines, want a header and two rows", len(lines))
	}

	// The last column must start at the same place on every line, measured
	// as a terminal would. The cell is looked for by name rather than as
	// "the last field", since a cell may contain a space.
	want := columnStart(t, lines[0], "RUNNERS")

	for _, row := range []struct{ line, cell string }{
		{lines[1], "4 runners"},
		{lines[2], "-"},
	} {
		plain := ansiPattern.ReplaceAllString(row.line, "")

		if got := strings.Index(plain, row.cell); got != want {
			t.Errorf("last column starts at %d on %q, want %d", got, plain, want)
		}
	}
}

// TestTableHasNoTrailingSpaces checks that nothing carries padding off the end
// of a line, which shows up as stray whitespace in a pipe or a diff.
func TestTableHasNoTrailingSpaces(t *testing.T) {
	tbl := newTable("A", "B")
	tbl.addRow("a-much-longer-cell", "x")
	tbl.addRow("a", "y")

	var buf bytes.Buffer
	tbl.write(&buf, palette{})

	for line := range strings.SplitSeq(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line != strings.TrimRight(line, " ") {
			t.Errorf("line has trailing spaces: %q", line)
		}
	}
}

func TestTableIndents(t *testing.T) {
	tbl := newTable("A", "B").indented(2)
	tbl.addRow("x", "y")

	var out bytes.Buffer
	tbl.write(&out, palette{})

	for line := range strings.SplitSeq(strings.TrimRight(out.String(), "\n"), "\n") {
		if !strings.HasPrefix(line, "  ") {
			t.Errorf("line is not indented: %q", line)
		}
		// Indenting must not reintroduce trailing padding.
		if line != strings.TrimRight(line, " ") {
			t.Errorf("line has trailing spaces: %q", line)
		}
	}
}

// columnStart is where a header begins, measured without its colour.
func columnStart(t *testing.T, line, header string) int {
	t.Helper()

	plain := ansiPattern.ReplaceAllString(line, "")

	i := strings.Index(plain, header)
	if i < 0 {
		t.Fatalf("header %q is not in %q", header, plain)
	}

	return i
}

func TestFieldsAlignLabels(t *testing.T) {
	var f fields
	f.add("GitHub", "https://github.com/my-org")
	f.add("Credentials", "a token")
	f.add("Identity", "a value", "a hint underneath")

	var buf bytes.Buffer
	f.write(&buf, palette{})

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("wrote %d lines, want 4", len(lines))
	}

	// Every colon lines up, so the values start in one column.
	want := strings.Index(lines[0], ":")
	for _, line := range lines[:3] {
		if got := strings.Index(line, ":"); got != want {
			t.Errorf("colon at %d in %q, want %d", got, line, want)
		}
	}

	// The extra value is indented under the first rather than labelled again.
	if strings.Contains(lines[3], ":") || !strings.HasPrefix(lines[3], "  ") {
		t.Errorf("the second value of a field is not indented under it: %q", lines[3])
	}
}

func TestWriteSectionCountsItems(t *testing.T) {
	var out bytes.Buffer
	writeSection(&out, palette{}, "Scale sets", 2)

	if got := out.String(); got != "Scale sets (2)\n" {
		t.Errorf("writeSection() = %q, want the title and its count", got)
	}
}
