// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package provider_test

import (
	"encoding/json"
	"errors"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
)

// TestOneOrMoreIsWrittenAsItWasRead checks one string is written back as a
// string, as it was before a list could be given, and several as a list.
func TestOneOrMoreIsWrittenAsItWasRead(t *testing.T) {
	tests := []struct {
		name, yaml, json string
	}{
		{"one", "a\n", `"a"`},
		{"several", "- a\n- b\n", `["a","b"]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var o provider.OneOrMore
			if err := yaml.Unmarshal([]byte(tt.yaml), &o); err != nil {
				t.Fatal(err)
			}

			if out, err := yaml.Marshal(o); err != nil || string(out) != tt.yaml {
				t.Errorf("YAML = %q, %v; want %q", out, err, tt.yaml)
			}
			if out, err := json.Marshal(o); err != nil || string(out) != tt.json {
				t.Errorf("JSON = %s, %v; want %s", out, err, tt.json)
			}
		})
	}

	t.Run("none", func(t *testing.T) {
		if out, err := json.Marshal(provider.OneOrMore(nil)); err != nil || string(out) != `""` {
			t.Errorf("JSON = %s, %v; want an empty string", out, err)
		}
	})
}

func TestOneOrMoreRefusesAMapping(t *testing.T) {
	var o provider.OneOrMore
	if err := yaml.Unmarshal([]byte("{a: b}"), &o); !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("Unmarshal() = %v, want an ErrInvalidArgument", err)
	}
}
