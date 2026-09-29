// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/konradasb/rungar/internal/config"
)

func newConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Print a configuration file as the daemon would run it, in YAML",
		Long: "Print the whole configuration in a file as the daemon would load it: every default " +
			"filled in, Rungar's and each provider type's, and anchors and merge keys resolved. Inline " +
			"secrets -- a GitHub token or App key, a provider's token -- are printed as REDACTED. " +
			"Runner blocks are printed as written, the provider's and each scale set's for it. Like " +
			"validate, it contacts nothing and needs no daemon, and it fails on a file the daemon " +
			"would not start from.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			configFile, _ := cmd.Flags().GetString("config")

			cfg, err := config.Load(configFile)
			if err != nil {
				return err
			}
			warnDeprecated(cmd.ErrOrStderr(), configFile, cfg.Deprecations())

			b, err := cfg.YAML()
			if err != nil {
				return err
			}

			_, err = cmd.OutOrStdout().Write(b)

			return err
		},
	}

	// Persistent, so that config migrate reads the same flag.
	addConfigFlag(cmd.PersistentFlags())
	cmd.AddCommand(newConfigMigrateCommand())

	return markNoDaemon(cmd)
}

func newConfigMigrateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Rewrite a configuration file for this version of Rungar",
		Long: "Rewrite a configuration file for the configuration version this rungar writes: its " +
			"version set, and every key or value that is deprecated replaced by what replaces it, so " +
			"that it means what it did and loads without a warning. It prints the file rewritten, and " +
			"what it changed to stderr; --write replaces the file instead, keeping its mode and owner. " +
			"Comments are kept, and only the version's line changes unless a deprecated key has to be " +
			"rewritten, which lays the whole file out afresh: look at the difference before replacing " +
			"the file. It fails, changing nothing, on a file that would not load, before or after. Like " +
			"validate, it contacts nothing and needs no daemon.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			configFile, _ := cmd.Flags().GetString("config")
			write, _ := cmd.Flags().GetBool("write")

			b, changes, err := config.Migrate(configFile)
			if err != nil {
				return err
			}

			stderr := cmd.ErrOrStderr()
			for _, c := range changes {
				_, _ = fmt.Fprintf(stderr, "Changed: %s: %s\n", configFile, c)
			}

			if !write {
				if len(changes) == 0 {
					_, _ = fmt.Fprintf(stderr, "%s is up to date: nothing to change\n", configFile)
				}
				_, err = cmd.OutOrStdout().Write(b)

				return err
			}

			if len(changes) == 0 {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s is up to date: nothing to change\n", configFile)
				return err
			}
			if err := replaceFile(configFile, b); err != nil {
				return err
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s is migrated to version %d: %s\n", configFile,
				config.Version, plural(len(changes), "change"))

			return err
		},
	}

	cmd.Flags().BoolP("write", "w", false, "Replace the file, rather than printing it")

	return markNoDaemon(cmd)
}

// warnDeprecated prints what is deprecated in a configuration file, and how to
// rewrite it.
func warnDeprecated(w io.Writer, configFile string, deprecations []config.Deprecation) {
	if len(deprecations) == 0 {
		return
	}

	for _, d := range deprecations {
		_, _ = fmt.Fprintf(w, "Warning: %s: %s\n", configFile, d)
	}
	_, _ = fmt.Fprintf(w, "Warning: rungar config migrate -f %s rewrites what is deprecated\n", configFile)
}
