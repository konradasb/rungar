// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestPaletteLeavesPlainOutputAlone checks nothing is painted when the output
// is not a terminal, so output piped to a file or a program carries no escape
// sequences.
func TestPaletteLeavesPlainOutputAlone(t *testing.T) {
	p := paletteFor(&bytes.Buffer{})

	for _, got := range []string{
		p.bold("x"), p.dim("x"), p.accent("x"), p.status(statusOK), p.paint(ansiRed, "x"),
	} {
		if strings.Contains(got, "\x1b") {
			t.Errorf("plain output carries an escape sequence: %q", got)
		}
	}
}

// TestPaletteColoursEachWordByItsMeaning checks each style and status word
// gets its colour and is reset after, an unknown word is left plain, and
// painting nothing writes nothing.
func TestPaletteColoursEachWordByItsMeaning(t *testing.T) {
	p := palette{enabled: true}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"bold", p.bold("x"), ansiBold},
		{"dim", p.dim("x"), ansiDim},
		{"accent", p.accent("x"), ansiCyan},
		{"ok is green", p.status(statusOK), ansiGreen},
		{"disabled is yellow", p.status(providerDisabled), ansiYellow},
		{"unreachable is red", p.status(statusUnreachable), ansiRed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.HasPrefix(tt.got, tt.want) {
				t.Errorf("got %q, want it to start with %q", tt.got, tt.want)
			}
			if !strings.HasSuffix(tt.got, ansiReset) {
				t.Errorf("got %q, want it reset afterwards", tt.got)
			}
		})
	}

	// A word the palette has no opinion about is left as it is, rather than
	// coloured at random.
	if got := p.status("SOMETHING ELSE"); got != "SOMETHING ELSE" {
		t.Errorf("status(unknown) = %q, want it plain", got)
	}

	// Painting nothing is nothing, not a bare escape sequence.
	if got := p.paint(ansiRed, ""); got != "" {
		t.Errorf("paint(empty) = %q, want empty", got)
	}
}

// TestVisibleWidthIgnoresColour checks a cell's width counts the characters
// shown, not the escape sequences colouring them or the bytes encoding them.
func TestVisibleWidthIgnoresColour(t *testing.T) {
	p := palette{enabled: true}

	tests := []struct {
		name string
		in   string
		want int
	}{
		{"plain", "OK", 2},
		{"coloured", p.status(statusOK), 2},
		{"bold", p.bold("NAME"), 4},
		{"nested", p.bold(p.accent("abc")), 3},
		{"empty", "", 0},
		{"multi-byte", "café ±1 µs", 10},
		{"coloured multi-byte", p.accent("µs"), 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := visibleWidth(tt.in); got != tt.want {
				t.Errorf("visibleWidth(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}
