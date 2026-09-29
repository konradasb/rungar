// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

//go:build e2e

package e2e

import (
	"context"
	"testing"
)

// provider is what the tests read of a provider in rungar providers --json.
type provider struct {
	Name               string `json:"name"`
	Reachable          bool   `json:"reachable"`
	Error              string `json:"error"`
	Disabled           bool   `json:"disabled"`
	ConfiguredDisabled bool   `json:"configured_disabled"`
}

// TestProvidersAreReachable checks the daemon reaches every configured
// provider.
func TestProvidersAreReachable(t *testing.T) {
	out := env.rungar(t, "providers", "ls", "--json")

	providers := decode[struct {
		Providers []provider `json:"providers"`
	}](t, out, "providers").Providers

	if len(providers) != len(env.providers) {
		t.Fatalf("providers = %+v, want %v", providers, env.providers)
	}
	for _, p := range providers {
		if !p.Reachable {
			t.Errorf("provider %s cannot be reached: %s", p.Name, p.Error)
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
