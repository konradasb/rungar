// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"io"
	"os"
	"regexp"
	"unicode/utf8"

	"golang.org/x/term"
)

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"
)

// ansiPattern matches an ANSI colour escape sequence.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// palette colours output written to a terminal. Colour only emphasises: it
// never carries meaning the text does not.
type palette struct {
	enabled bool
}

// paletteFor returns the palette for w: coloured if w is a terminal and
// NO_COLOR is unset.
func paletteFor(w io.Writer) palette {
	return palette{enabled: isTerminal(w) && os.Getenv("NO_COLOR") == ""}
}

func (p palette) paint(code, s string) string {
	if !p.enabled || s == "" {
		return s
	}

	return code + s + ansiReset
}

func (p palette) bold(s string) string { return p.paint(ansiBold, s) }

// dim marks secondary text, such as a note beside a value.
func (p palette) dim(s string) string { return p.paint(ansiDim, s) }

// accent marks names.
func (p palette) accent(s string) string { return p.paint(ansiCyan, s) }

// failure marks an error shown in place of a value.
func (p palette) failure(s string) string { return p.paint(ansiRed, s) }

// status colours a status word: green for working, yellow for in between, red
// for broken. Other words are left plain.
func (p palette) status(word string) string {
	switch word {
	case statusOK, machineRunning, gitHubIdle, gitHubBusy, scaleSetListening,
		eventCreated, eventAdopted, eventResumed:
		return p.paint(ansiGreen, word)
	case runnerStarting, machineStopped, gitHubOffline, statusLeftover,
		statusDisabled, statusDraining, statusHeld,
		scaleSetStarting, scaleSetWaiting, scaleSetHoldingBack, scaleSetPaused,
		eventLost, eventFull, eventFailing, eventPaused:
		return p.paint(ansiYellow, word)
	case gitHubUnregistered, statusUnreachable:
		return p.paint(ansiRed, word)
	default:
		return word
	}
}

// isTerminal reports whether stream, a command's input or output, is a
// terminal.
func isTerminal(stream any) bool {
	f, ok := stream.(*os.File)

	return ok && term.IsTerminal(int(f.Fd()))
}

// visibleWidth returns how many columns s takes on a terminal, ignoring colour
// escape sequences.
func visibleWidth(s string) int {
	return utf8.RuneCountInString(ansiPattern.ReplaceAllString(s, ""))
}
