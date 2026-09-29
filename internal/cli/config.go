// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"github.com/spf13/cobra"

	"github.com/konradasb/rungar/internal/config"
)

func newConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "config",
		Annotations: noDaemon,
		Short:       "Print a configuration file as the daemon would run it, in YAML",
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

			b, err := cfg.YAML()
			if err != nil {
				return err
			}

			_, err = cmd.OutOrStdout().Write(b)

			return err
		},
	}

	cmd.Flags().StringP("config", "f", config.DefaultPath, "Configuration file")

	return cmd
}
