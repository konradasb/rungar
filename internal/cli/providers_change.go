// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"slices"
	"strings"
	"time"

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
	cmd := &cobra.Command{
		Use:   use + " NAME",
		Short: short,
		Long: long + " It lasts until the daemon restarts, which goes back to what the configuration " +
			"says: set disabled there to make it last.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd, func(client rungarv1.RungarServiceClient) error {
				return changeProvider(cmd, client, args[0], change)
			})
		},
	}

	return cmd
}

// changeProvider has the daemon make the change, and waits for the provider to
// drain if asked to.
func changeProvider(cmd *cobra.Command, client rungarv1.RungarServiceClient, name string, change providerChange,
) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	st, err := client.GetStatus(ctx, &rungarv1.GetStatusRequest{})
	if err != nil {
		return err
	}

	pr, err := findProvider(st, name)
	if err != nil {
		return err
	}

	asConfigured := change.disable == pr.GetConfiguredDisabled()

	if pr.GetDisabled() == change.disable {
		_, _ = fmt.Fprintf(out, "Provider %s is already %s.\n", name, enabledWord(change.disable))
	} else {
		if change.disable {
			_, err = client.DisableProvider(ctx, &rungarv1.DisableProviderRequest{Name: name})
		} else {
			_, err = client.EnableProvider(ctx, &rungarv1.EnableProviderRequest{Name: name})
		}
		if err != nil {
			return err
		}

		if asConfigured {
			_, _ = fmt.Fprintf(out, "Provider %s is %s, as the configuration has it.\n", name, enabledWord(change.disable))
		} else {
			_, _ = fmt.Fprintf(out, "Provider %s is %s until the daemon restarts.\n", name, enabledWord(change.disable))
		}
	}

	if change.drain {
		return waitForNoRunners(cmd, client, &rungarv1.ListRunnersRequest{Provider: name}, []string{name},
			"on "+name, name+" is drained: no runner is left on it")
	}

	return nil
}

// findProvider returns the provider of this name in st.
func findProvider(st *rungarv1.DaemonStatus, name string) (*rungarv1.Provider, error) {
	providers := st.GetProviders()

	i := slices.IndexFunc(providers, func(p *rungarv1.Provider) bool { return p.GetName() == name })
	if i >= 0 {
		return providers[i], nil
	}

	names := make([]string, 0, len(providers))
	for _, p := range providers {
		names = append(names, p.GetName())
	}

	return nil, errdefs.NotFound("no provider %q in the configuration; it has %s", name, strings.Join(names, ", "))
}

func enabledWord(disabled bool) string {
	if disabled {
		return "disabled"
	}

	return "enabled"
}

// waitForNoRunners polls until req lists no runner, writing how many are left
// whenever that changes, and done once none is. It cannot be sure while one of
// providers cannot be listed. where says whose runners they are: "on
// compute2".
func waitForNoRunners(cmd *cobra.Command, client rungarv1.RungarServiceClient, req *rungarv1.ListRunnersRequest,
	providers []string, where, done string,
) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	last := -1

	for {
		resp, err := client.ListRunners(ctx, req)
		if err != nil {
			return err
		}

		var unreachable []string
		for _, name := range providers {
			if _, ok := resp.GetUnreachableProviders()[name]; ok {
				unreachable = append(unreachable, name)
			}
		}

		switch n := len(resp.GetRunners()); {
		case len(unreachable) > 0:
			_, _ = fmt.Fprintf(out, "%s cannot be listed; trying again\n", strings.Join(unreachable, ", "))
		case n == 0:
			_, _ = fmt.Fprintln(out, done)
			return nil
		case n != last:
			_, _ = fmt.Fprintf(out, "Waiting for %s %s to finish\n", plural(n, "runner"), where)
			last = n
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}
