// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Command docgen generates the documentation site's reference pages from the
// code: the command line from the commands, the configuration from the doc
// comments on the types it is read into, and the metrics from those the daemon
// registers.
//
// make docs-gen runs it, and CI checks its committed output is current. A
// configuration field without a doc comment fails it.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	out := flag.String("out", "docs/content/docs", "The site's content/docs directory")
	root := flag.String("root", ".", "The module's root, whose packages the configuration is read from")
	flag.Parse()

	commands := commandPaths()

	steps := []struct {
		name string
		run  func() error
	}{
		{"command line", func() error { return writeCLI(filepath.Join(*out, "reference", "cli"), commands) }},
		{"configuration", func() error { return writeConfiguration(*root, *out, commands) }},
		{"metrics", func() error { return writeMetrics(filepath.Join(*out, "reference")) }},
	}

	for _, step := range steps {
		if err := step.run(); err != nil {
			fmt.Fprintf(os.Stderr, "docgen: %s: %v\n", step.name, err)
			os.Exit(1)
		}
	}
}
