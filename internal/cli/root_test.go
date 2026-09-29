// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestNoArgumentsPrintsHelp checks the command tree builds, and that no
// arguments prints help rather than doing anything.
func TestNoArgumentsPrintsHelp(t *testing.T) {
	cmd := NewCommand()

	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("rungar with no arguments = %v, want help", err)
	}

	body := out.String()
	for _, want := range []string{"rungar", "serve", "status"} {
		if !strings.Contains(body, want) {
			t.Errorf("the help does not mention %q:\n%s", want, body)
		}
	}
}

// TestUnknownSubcommandFails checks a mistyped command is an error.
func TestUnknownSubcommandFails(t *testing.T) {
	cmd := NewCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"serv"})

	if err := cmd.Execute(); err == nil {
		t.Fatal("an unknown subcommand was accepted")
	}
}

// TestErrorsAreThePlainMessage checks an error the daemon sends reads as its
// message alone.
func TestErrorsAreThePlainMessage(t *testing.T) {
	h := newHarness(t)

	err := h.run("scale-sets", "inspect", "nowhere")
	if got := errorMessage(err); got != `no scale set "nowhere"` {
		t.Errorf("errorMessage() = %q, want the daemon's words alone", got)
	}
}

// TestOnlyLocalCommandsAreMarkedNoDaemon checks the commands that read a
// configuration file need no daemon, and that the rest ask it.
func TestOnlyLocalCommandsAreMarkedNoDaemon(t *testing.T) {
	root := NewCommand()

	for _, tc := range []struct {
		args []string
		asks bool
	}{
		{[]string{"serve"}, false},
		{[]string{"validate"}, false},
		{[]string{"config"}, false},
		{[]string{"config", "migrate"}, false},
		{[]string{"status"}, true},
		{[]string{"scale-sets", "pause"}, true},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			cmd, _, err := root.Find(tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if got := AsksDaemon(cmd); got != tc.asks {
				t.Errorf("AsksDaemon() = %v, want %v", got, tc.asks)
			}
		})
	}
}
