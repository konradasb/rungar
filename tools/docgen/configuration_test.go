// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package main

import (
	"testing"

	"github.com/konradasb/rungar/internal/config"
)

func TestAsCodeMarksOnlyWhatItKnows(t *testing.T) {
	keys := map[string]bool{"max_runners": true}
	commands := map[string]bool{"rungar": true, "rungar config": true, "rungar config migrate": true}

	tests := []struct {
		name, in, want string
	}{
		{"known key", "Set max_runners to 2.", "Set `max_runners` to 2."},
		{"unknown key", "Set min_runners to 2.", "Set min_runners to 2."},
		{"longest command", "Run rungar config migrate first.", "Run `rungar config migrate` first."},
		{"command and words", "rungar config rewrites it.", "`rungar config` rewrites it."},
		{"flag", "Pass --write to replace it.", "Pass `--write` to replace it."},
		{"path", "Read from /etc/rungar/config.yaml.", "Read from `/etc/rungar/config.yaml`."},
		{"environment variable", "Unless RUNGAR_SOCKET is set.", "Unless `RUNGAR_SOCKET` is set."},
		{"already code", "Set `max_runners` and --write.", "Set `max_runners` and `--write`."},
		{"inside code", "Run `rungar config migrate --write`.", "Run `rungar config migrate --write`."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := asCode(tt.in, keys, commands); got != tt.want {
				t.Errorf("asCode(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestProseWritesFieldNamesAsKeys(t *testing.T) {
	keys := map[string]string{"TokenPath": "token_path", "Token": "token", "GitHub": "github"}

	tests := []struct {
		name, in, want string
	}{
		{"first word", "Token is the token.", "`token` is the token."},
		{"compound name", "Unless TokenPath is set.", "Unless `token_path` is set."},
		{"single word left alone", "A Token here.", "A Token here."},
		{"GitHub left as the name", "Asks GitHub.", "Asks GitHub."},
		{"lines joined", "One\nline.", "One line."},
		{"example kept", "For example:\n\n\tname: x\n\tsize: 2", "For example:\n\n```yaml\nname: x\nsize: 2\n```"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := prose(tt.in, keys); got != tt.want {
				t.Errorf("prose(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestAnchorJoinsKeyPathWithHyphens checks a key's path becomes the anchor
// Markdown renderers give its heading.
func TestAnchorJoinsKeyPathWithHyphens(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"version", "version"},
		{"max_runners", "max-runners"},
		{"github.app.key_path", "github-app-key-path"},
		{"scale_sets[].runner", "scale-sets-runner"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := anchor(tt.in); got != tt.want {
				t.Errorf("anchor(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestPackageNameIsTheLastPathElement checks an import path's package name
// drops the path and any version suffix.
func TestPackageNameIsTheLastPathElement(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"time", "time"},
		{"github.com/konradasb/rungar/internal/provider/gcp", "gcp"},
		{"gopkg.in/yaml.v3", "yaml"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := packageName(tt.in); got != tt.want {
				t.Errorf("packageName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestPluralLeavesDescribedTypesAlone checks a bare type name takes an s, and
// one already plural or carrying a description is left as it is.
func TestPluralLeavesDescribedTypesAlone(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"string", "strings"},
		{"mappings", "mappings"},
		{"duration, such as 30s or 5m", "duration, such as 30s or 5m"},
		{"string: spread or pack", "string: spread or pack"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := plural(tt.in); got != tt.want {
				t.Errorf("plural(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestEveryProviderTypeHasAPage checks each provider type internal/config
// registers has an entry in providerTypeDocs, which docgen otherwise fails on.
func TestEveryProviderTypeHasAPage(t *testing.T) {
	pages, err := configurationPages(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	types := config.ProviderTypes()
	if len(types) == 0 || len(pages) != len(types)+1 {
		t.Errorf("%d pages for %d provider types, want one each and the daemon's", len(pages), len(types))
	}
}
