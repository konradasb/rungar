// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Command rungar is the Rungar daemon and its command line. rungar serve keeps
// GitHub Actions runner scale sets supplied with runners, each a machine on one
// of its providers; the other commands ask the running daemon what it is doing.
package main

import (
	"os"

	"github.com/konradasb/rungar/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
