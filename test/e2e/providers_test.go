// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

//go:build e2e

package e2e

import (
	"context"
	"slices"
	"testing"
)

// provider is what the tests read of a provider in rungar providers ls
// --json.
type provider struct {
	Name               string `json:"name"`
	Reachable          bool   `json:"reachable"`
	Error              string `json:"error"`
	Disabled           bool   `json:"disabled"`
	ConfiguredDisabled bool   `json:"configured_disabled"`
}

// TestProvidersAreReachable checks the daemon reaches every provider the
// scale set uses. The configuration keeps all of the base's providers, so
// others may be listed too when RUNGAR_E2E_PROVIDERS names a subset.
func TestProvidersAreReachable(t *testing.T) {
	out := env.rungar(t, "providers", "ls", "--json")

	providers := decode[struct {
		Providers []provider `json:"providers"`
	}](t, out, "providers").Providers

	for _, name := range env.providers {
		i := slices.IndexFunc(providers, func(p provider) bool { return p.Name == name })
		switch {
		case i < 0:
			t.Errorf("provider %s is not listed in %+v", name, providers)
		case !providers[i].Reachable:
			t.Errorf("provider %s is unreachable: %s", name, providers[i].Error)
		}
	}
}

// TestDisableAndEnableAProvider checks a provider can be taken out of
// placement and put back while the daemon runs. It is the daemon under
// test's view alone: a real Rungar on the same provider is unaffected.
func TestDisableAndEnableAProvider(t *testing.T) {
	name := env.providers[len(env.providers)-1]

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		_, _ = env.runRungar(ctx, "providers", "enable", name)
	})

	env.rungar(t, "providers", "disable", name)

	if p := env.inspectProvider(t, name); !p.Disabled || p.ConfiguredDisabled {
		t.Errorf("%s = %+v, want it disabled until the daemon restarts", name, p)
	}

	env.rungar(t, "providers", "enable", name)

	if p := env.inspectProvider(t, name); p.Disabled {
		t.Errorf("%s = %+v, want it enabled again", name, p)
	}
}

// inspectProvider reads one provider.
func (e *environment) inspectProvider(t *testing.T, name string) provider {
	t.Helper()

	out := e.rungar(t, "providers", "inspect", name, "--json")

	return decode[struct {
		Provider provider `json:"provider"`
	}](t, out, "the provider").Provider
}
