// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/konradasb/rungar/internal/cli"
)

// TestNoArgumentsPrintsHelp checks the command tree builds, and that no
// arguments prints help rather than doing anything.
func TestNoArgumentsPrintsHelp(t *testing.T) {
	cmd := cli.NewCommand()

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
	cmd := cli.NewCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"serv"})

	if err := cmd.Execute(); err == nil {
		t.Fatal("an unknown subcommand was accepted")
	}
}
