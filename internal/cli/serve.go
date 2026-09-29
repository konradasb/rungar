// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"github.com/spf13/cobra"

	"github.com/konradasb/rungar/internal/config"
	"github.com/konradasb/rungar/internal/daemon"
)

func newServeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "serve",
		Annotations: noDaemon,
		Short:       "Run the daemon",
		Long: "Run the daemon until SIGINT or SIGTERM. Stopping leaves the runners where they are, " +
			"and starting adopts them, so restart it to take up a changed configuration: a running " +
			"job is not disturbed. The other commands ask it what it is doing through the socket it " +
			"serves them on.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			configFile, _ := cmd.Flags().GetString("config")

			return daemon.Serve(cmd.Context(), configFile)
		},
	}

	cmd.Flags().StringP("config", "f", config.DefaultPath, "Configuration file")

	return cmd
}
