// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/konradasb/rungar/internal/config"
)

func newValidateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Check a configuration file, without contacting GitHub or any provider",
		Long: "Check a configuration file as the daemon would load it: every key, the providers' parts " +
			"and every scale set's runner block, and the files it names -- the GitHub credential and " +
			"each provider's own. Nothing is contacted and no daemon is needed, so it can be run " +
			"anywhere the files are. It exits non-zero if the daemon would not start from the file, and " +
			"warns, on stderr, of anything in it that is deprecated, which rungar config migrate rewrites.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			configFile, _ := cmd.Flags().GetString("config")

			cfg, err := config.Load(configFile)
			if err != nil {
				return err
			}
			if err := cfg.CheckFiles(); err != nil {
				return err
			}
			warnDeprecated(cmd.ErrOrStderr(), configFile, cfg.Deprecations())

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s is valid: %s, %s\n", configFile,
				plural(len(cfg.Providers), "provider"), plural(len(cfg.ScaleSets), "scale set"))

			return err
		},
	}

	addConfigFlag(cmd.Flags())

	return markNoDaemon(cmd)
}
