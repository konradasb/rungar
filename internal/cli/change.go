// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"io"
	"strings"
)

// untilRestart ends the long description of a command that changes the
// running daemon only: key is the configuration's setting that makes the
// change last.
func untilRestart(key string) string {
	return " It lasts until the daemon restarts, which goes back to what the configuration says: set " +
		key + " there to make it last."
}

// writeChange writes the state a pause, resume, disable or enable left kind
// name in: already so, so as the configuration has it, or so until the daemon
// restarts.
func writeChange(out io.Writer, kind, name, state string, already, asConfigured bool) {
	switch {
	case already:
		_, _ = fmt.Fprintf(out, "%s %s is already %s.\n", kind, name, state)
	case asConfigured:
		_, _ = fmt.Fprintf(out, "%s %s is %s, as the configuration has it.\n", kind, name, state)
	default:
		_, _ = fmt.Fprintf(out, "%s %s is %s until the daemon restarts.\n", kind, name, state)
	}
}

// configuredNames lists names for an error saying what the configuration has
// instead: "a, b", or "none".
func configuredNames(names []string) string {
	if len(names) == 0 {
		return "none"
	}

	return strings.Join(names, ", ")
}
