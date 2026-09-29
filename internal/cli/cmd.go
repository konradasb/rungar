// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package cli is the rungar command line. rungar serve runs the daemon, and
// rungar validate and rungar config check and print a configuration file;
// every other command asks the running daemon over its socket.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc/status"

	"github.com/konradasb/rungar/internal/config"
	"github.com/konradasb/rungar/internal/version"
)

// pollInterval is how often drain, and scale-sets pause --wait and rm --wait,
// look again.
var pollInterval = 5 * time.Second

// Execute runs the rungar command line and returns the process exit status.
// SIGINT and SIGTERM cancel the command's context.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return exitStatus(ctx, NewCommand(), os.Stderr)
}

// noDaemon annotates a command that does not ask the daemon, and so has no
// use for --socket.
var noDaemon = map[string]string{"rungar.sh/no-daemon": "true"}

// AsksDaemon reports whether cmd asks the running daemon, through its socket.
func AsksDaemon(cmd *cobra.Command) bool {
	_, ok := cmd.Annotations["rungar.sh/no-daemon"]
	return !ok
}

// NewCommand returns the root rungar command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "rungar",
		Short:         "Run GitHub Actions runners, a fresh machine for every job, on your own hosts and in the cloud",
		SilenceErrors: true,
		SilenceUsage:  true,
		Version:       version.Version,
	}

	cmd.SetVersionTemplate(version.String() + "\n")
	cmd.PersistentFlags().String("socket", "",
		"The daemon's socket (default $"+socketEnv+", or "+config.DefaultSocket+")")

	cmd.AddCommand(newServeCommand(), newValidateCommand(), newConfigCommand(), newStatusCommand(), newReconcileCommand(),
		newScaleSetsCommand(), newRunnersCommand(), newProvidersCommand(), newEventsCommand())

	return cmd
}

// exitStatus runs cmd, prints any error to stderr, and returns the exit status.
func exitStatus(ctx context.Context, cmd *cobra.Command, stderr io.Writer) int {
	if err := cmd.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %s\n", errorMessage(err))
		return 1
	}

	return 0
}

// errorMessage returns err as printed for a person: a gRPC status wrapped
// anywhere in err is replaced by its message, without the code.
func errorMessage(err error) string {
	var s interface {
		error
		GRPCStatus() *status.Status
	}
	if !errors.As(err, &s) {
		return err.Error()
	}

	return strings.Replace(err.Error(), s.Error(), s.GRPCStatus().Message(), 1)
}
