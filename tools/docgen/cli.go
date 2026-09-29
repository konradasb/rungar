// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/konradasb/rungar/internal/cli"
)

// commandGroups orders the root's commands on the section's index. A command
// in none of them fails docgen, so that none goes unlisted.
var commandGroups = []struct {
	title string
	names []string
}{
	{"Running the daemon", []string{"serve"}},
	{"Checking a configuration", []string{"validate", "config"}},
	{"Seeing what the daemon does", []string{"status", "events"}},
	{"Changing what the daemon does", []string{"scale-sets", "providers", "runners", "reconcile"}},
}

// cliIntro opens the section's index.
const cliIntro = "`rungar` is the daemon and its command line in one binary. `rungar serve` runs the " +
	"daemon; `rungar validate` and `rungar config` read a configuration file and need nothing else. " +
	"Every other command asks the running daemon, through its socket, which root and the `rungar` " +
	"group alone may use: run them with `sudo -u rungar`, or as root."

// writeCLI replaces dir with a page per command; the root command's is the
// section's index. commands are every command's path, which the text writes as
// code.
func writeCLI(dir string, commands map[string]bool) error {
	root := cli.NewCommand()
	root.InitDefaultVersionFlag()

	if err := os.RemoveAll(dir); err != nil {
		return err
	}

	index, err := cliIndex(root, commands)
	if err != nil {
		return err
	}

	indexMeta := meta{
		title: "Command line", weight: 3, icon: "terminal",
		description: "Every rungar command, with its flags.",
		collapsed:   true,
	}
	if err := writePage(filepath.Join(dir, "_index.md"), indexMeta, index); err != nil {
		return err
	}

	for _, cmd := range descendants(root) {
		page := commandPage(cmd, commands)
		m := meta{title: cmd.CommandPath(), description: cmd.Short}

		if err := writePage(filepath.Join(dir, pageName(cmd)+".md"), m, page); err != nil {
			return err
		}
	}

	return nil
}

// commandPaths returns every rungar command's path, the root's included.
func commandPaths() map[string]bool {
	root := cli.NewCommand()

	paths := map[string]bool{root.CommandPath(): true}
	for _, c := range descendants(root) {
		paths[c.CommandPath()] = true
	}

	return paths
}

// descendants returns every command under cmd that a person can run or
// group others under, depth first.
func descendants(cmd *cobra.Command) []*cobra.Command {
	var out []*cobra.Command

	for _, c := range cmd.Commands() {
		if !c.IsAvailableCommand() {
			continue
		}
		out = append(out, c)
		out = append(out, descendants(c)...)
	}

	return out
}

// pageName returns the page a command is documented on: rungar_runners_ls
// for rungar runners ls.
func pageName(cmd *cobra.Command) string {
	return strings.ReplaceAll(cmd.CommandPath(), " ", "_")
}

// commandLink returns a link to a command's page, its path as the text.
func commandLink(cmd *cobra.Command) string {
	return fmt.Sprintf("[`%s`]({{< relref \"/docs/reference/cli/%s\" >}})", cmd.CommandPath(), pageName(cmd))
}

// cliIndex returns the section index's body: what rungar is, its commands by
// what they are for, and the flags every command takes.
func cliIndex(root *cobra.Command, commands map[string]bool) ([]byte, error) {
	var b bytes.Buffer

	b.WriteString(cliIntro + "\n\n## Commands\n")

	listed := map[string]bool{}
	for _, g := range commandGroups {
		fmt.Fprintf(&b, "\n### %s\n\n| Command | Description |\n|---|---|\n", g.title)

		for _, name := range g.names {
			cmd, _, err := root.Find([]string{name})
			if err != nil || cmd == root {
				return nil, fmt.Errorf("group %q names %q, which is not a command", g.title, name)
			}
			listed[name] = true

			for _, c := range append([]*cobra.Command{cmd}, descendants(cmd)...) {
				fmt.Fprintf(&b, "| %s | %s. |\n", commandLink(c), c.Short)
			}
		}
	}

	for _, c := range root.Commands() {
		if c.IsAvailableCommand() && !listed[c.Name()] {
			return nil, fmt.Errorf("command %q is in no group: add it to commandGroups", c.Name())
		}
	}

	b.WriteString("\n## Global flags\n\n")
	writeFlags(&b, root.PersistentFlags(), "The commands that ask the daemon take `--socket`; every command takes `-h`, `--help`.",
		commands)
	b.WriteString("\n`rungar --version` prints the version.\n")

	return b.Bytes(), nil
}

// commandPage returns a command's page body: what it does, how it is run, its
// flags, and, for a group, its commands.
func commandPage(cmd *cobra.Command, commands map[string]bool) []byte {
	var b bytes.Buffer

	// The usage line offers [flags] for a command's own flags alone, not
	// --socket, which every command that asks the daemon takes.
	useLine := cmd.UseLine()
	own, global := commandFlags(cmd)
	if own.HasAvailableFlags() && !strings.Contains(useLine, "[flags]") {
		useLine += " [flags]"
	}

	text := cmd.Long
	if text == "" {
		text = cmd.Short + "."
	}
	b.WriteString(asCode(text, nil, commands) + "\n")

	if cmd.Runnable() {
		fmt.Fprintf(&b, "\n## Usage\n\n```console\n$ %s\n```\n", useLine)
	}

	if cmd.Example != "" {
		fmt.Fprintf(&b, "\n## Examples\n\n```console\n%s\n```\n", strings.TrimRight(cmd.Example, "\n"))
	}

	if subs := descendants(cmd); len(subs) > 0 {
		b.WriteString("\n## Commands\n\n| Command | Description |\n|---|---|\n")
		for _, c := range subs {
			fmt.Fprintf(&b, "| %s | %s. |\n", commandLink(c), c.Short)
		}
	}

	if !cmd.Runnable() {
		return b.Bytes()
	}

	if own.HasAvailableFlags() {
		b.WriteString("\n## Flags\n\n")
		writeFlags(&b, own, "", commands)
	}

	if cli.AsksDaemon(cmd) {
		b.WriteString("\n## Global flags\n\n")
		writeFlags(&b, global, "", commands)
	}

	return b.Bytes()
}

// commandFlags returns a command's own flags, and the global ones it takes:
// those the root declares for every command. Own flags are those it declares,
// and those a parent below the root declares for its commands, such as
// rungar config's --config; --help, which every command has, is neither.
func commandFlags(cmd *cobra.Command) (own, global *pflag.FlagSet) {
	own = pflag.NewFlagSet(cmd.Name(), pflag.ContinueOnError)
	global = pflag.NewFlagSet(cmd.Name(), pflag.ContinueOnError)
	rootFlags := cmd.Root().PersistentFlags()

	cmd.NonInheritedFlags().VisitAll(func(f *pflag.Flag) {
		if f.Name != "help" {
			own.AddFlag(f)
		}
	})
	cmd.InheritedFlags().VisitAll(func(f *pflag.Flag) {
		switch {
		case f.Name == "help":
		case rootFlags.Lookup(f.Name) != nil:
			global.AddFlag(f)
		default:
			own.AddFlag(f)
		}
	})

	return own, global
}

// writeFlags writes a table of flags, sorted by name, and a note under it.
// commands are every command's path, which the descriptions write as code.
func writeFlags(b *bytes.Buffer, flags *pflag.FlagSet, note string, commands map[string]bool) {
	var rows []string

	flags.VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Name == "version" {
			return
		}

		varname, usage := pflag.UnquoteUsage(f)

		name := "`--" + f.Name
		if varname != "" {
			name += " " + varname
		}
		name += "`"
		if f.Shorthand != "" {
			name = "`-" + f.Shorthand + "`, " + name
		}

		text := asCode(strings.TrimSuffix(usage, ".")+".", nil, commands)
		if def := f.DefValue; def != "" && def != "false" && def != "0" && def != "[]" {
			text += " Default: `" + def + "`."
		}

		rows = append(rows, fmt.Sprintf("| %s | %s |", name, text))
	})

	b.WriteString("| Flag | Description |\n|---|---|\n")
	for _, r := range rows {
		b.WriteString(r + "\n")
	}

	if note != "" {
		b.WriteString("\n" + note + "\n")
	}
}
