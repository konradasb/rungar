// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"io"
	"slices"

	"github.com/spf13/cobra"

	"github.com/konradasb/rungar/internal/errdefs"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// providerChange is what rungar providers disable, drain or enable does.
type providerChange struct {
	disable bool // disable the provider, rather than enable it
	drain   bool // then wait until no runner is left on it
}

func newProviderDisableCommand() *cobra.Command {
	return newProviderChangeCommand("disable", "Take a provider out of placement, leaving its runners to finish",
		"Take a provider out of placement: no new runner goes to it, and the runners on it are left to "+
			"finish their jobs.", providerChange{disable: true})
}

func newProviderDrainCommand() *cobra.Command {
	return newProviderChangeCommand("drain", "Disable a provider, and wait until its runners are gone",
		"Disable a provider, as disable does, and wait until the last runner on it has gone.",
		providerChange{disable: true, drain: true})
}

func newProviderEnableCommand() *cobra.Command {
	return newProviderChangeCommand("enable", "Put a provider back into placement",
		"Put a disabled provider back into placement.", providerChange{})
}

func newProviderChangeCommand(use, short, long string, change providerChange) *cobra.Command {
	return &cobra.Command{
		Use:   use + " NAME",
		Short: short,
		Long:  long + untilRestart("disabled"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd, func(client rungarv1.RungarServiceClient) error {
				return changeProvider(cmd.Context(), cmd.OutOrStdout(), client, args[0], change)
			})
		},
	}
}

// changeProvider has the daemon make the change to a provider, and waits for
// it to drain if asked to.
func changeProvider(ctx context.Context, out io.Writer, client rungarv1.RungarServiceClient, name string,
	change providerChange,
) error {
	st, err := client.GetStatus(ctx, &rungarv1.GetStatusRequest{})
	if err != nil {
		return err
	}

	pr, err := findProvider(st, name)
	if err != nil {
		return err
	}

	already := pr.GetDisabled() == change.disable
	if !already {
		if change.disable {
			_, err = client.DisableProvider(ctx, &rungarv1.DisableProviderRequest{Name: name})
		} else {
			_, err = client.EnableProvider(ctx, &rungarv1.EnableProviderRequest{Name: name})
		}
		if err != nil {
			return err
		}
	}
	writeChange(out, "Provider", name, disabledOrEnabled(change.disable), already,
		change.disable == pr.GetConfiguredDisabled())

	if change.drain {
		return waitForNoRunners(ctx, out, client, &rungarv1.ListRunnersRequest{Provider: name}, []string{name},
			"on "+name, name+" is drained: no runner is left on it")
	}

	return nil
}

// findProvider returns the provider of this name in st, or an
// errdefs.ErrNotFound naming those there are.
func findProvider(st *rungarv1.DaemonStatus, name string) (*rungarv1.Provider, error) {
	providers := st.GetProviders()

	i := slices.IndexFunc(providers, func(pr *rungarv1.Provider) bool { return pr.GetName() == name })
	if i >= 0 {
		return providers[i], nil
	}

	names := make([]string, 0, len(providers))
	for _, pr := range providers {
		names = append(names, pr.GetName())
	}

	return nil, errdefs.NotFound("no provider %q in the configuration; it has %s", name, configuredNames(names))
}

// disabledOrEnabled returns the state disabling, or else enabling, leaves a
// provider in.
func disabledOrEnabled(disable bool) string {
	if disable {
		return "disabled"
	}

	return "enabled"
}
