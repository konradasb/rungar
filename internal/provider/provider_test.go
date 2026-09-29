// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package provider_test

import (
	"errors"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
)

func node(t *testing.T, body string) *provider.Node {
	t.Helper()

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}

	return doc.Content[0]
}

type settings struct {
	Address string `yaml:"address"`
}

func TestDecode(t *testing.T) {
	var s settings
	if err := provider.Decode(node(t, "address: 10.0.0.1:7443\n"), &s); err != nil {
		t.Fatalf("Decode() = %v", err)
	}
	if s.Address != "10.0.0.1:7443" {
		t.Errorf("address = %q", s.Address)
	}
}

// TestDecodeRefusesWhatItDoesNotKnow checks that a provider's part of the
// configuration is as strict as the daemon's own: a misspelt setting is an
// error, not a setting silently left at its default.
func TestDecodeRefusesWhatItDoesNotKnow(t *testing.T) {
	var s settings
	if err := provider.Decode(node(t, "addr: 10.0.0.1:7443\n"), &s); !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("Decode() = %v, want an ErrInvalidArgument", err)
	}
}

func TestDecodeOfNothing(t *testing.T) {
	s := settings{Address: "kept"}
	if err := provider.Decode(nil, &s); err != nil || s.Address != "kept" {
		t.Errorf("Decode(nil) = %v, left %+v; want nothing changed", err, s)
	}
	if err := provider.Decode(&provider.Node{}, &s); err != nil || s.Address != "kept" {
		t.Errorf("Decode(empty) = %v, left %+v; want nothing changed", err, s)
	}
}

// TestMerge checks how a scale set's runner block is written over its
// provider's, which is the same for every type of provider.
func TestMerge(t *testing.T) {
	base := node(t, `
image: runner:latest
vcpus: 2
env: {HTTPS_PROXY: "http://proxy:3128", LANG: C}
mounts: [{type: volume, source: cache, target: /cache}]
`)
	over := node(t, `
vcpus: 8
env: {LANG: C.UTF-8, CI: "true"}
mounts: [{type: tmpfs, target: /tmp}]
image: null
`)
	baseBefore, _ := yaml.Marshal(base)

	got := map[string]any{}
	if err := provider.Merge(base, over).Decode(&got); err != nil {
		t.Fatal(err)
	}

	want := map[string]any{
		// Cleared by null.
		"image": nil,
		// Replaced.
		"vcpus": 8,
		// Merged key by key.
		"env": map[string]any{"HTTPS_PROXY": "http://proxy:3128", "LANG": "C.UTF-8", "CI": "true"},
		// A list replaced whole.
		"mounts": []any{map[string]any{"type": "tmpfs", "target": "/tmp"}},
	}
	if gotYAML, wantYAML := mustMarshal(t, got), mustMarshal(t, want); gotYAML != wantYAML {
		t.Errorf("Merge() =\n%s\nwant\n%s", gotYAML, wantYAML)
	}

	if after, _ := yaml.Marshal(base); string(after) != string(baseBefore) {
		t.Error("Merge() changed its base, which every scale set on the provider shares")
	}
}

func TestMergeOfNothing(t *testing.T) {
	base := node(t, "vcpus: 2\n")

	for name, got := range map[string]*provider.Node{
		"no scale set block": provider.Merge(base, &provider.Node{}),
		"no provider block":  provider.Merge(&provider.Node{}, base),
		"an empty block":     provider.Merge(base, node(t, "{}")),
	} {
		if s := mustMarshal(t, got); s != "vcpus: 2\n" {
			t.Errorf("%s: Merge() = %q, want the other block", name, s)
		}
	}

	if got := provider.Merge(nil, nil); got != nil {
		t.Errorf("Merge(nil, nil) = %v, want nil", got)
	}
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()

	b, err := yaml.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

// TestResolve checks settings shared with an anchor and a merge key are read
// as if written out: the entry's own keys winning, and nothing left pointing
// at the document the anchor was in.
func TestResolve(t *testing.T) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(`
- &dicer {type: dicer, address: a:1, tls: {ca_file: /ca.pem}}
- {<<: *dicer, address: b:1}
`), &doc); err != nil {
		t.Fatal(err)
	}

	got := map[string]any{}
	if err := provider.Resolve(doc.Content[0].Content[1]).Decode(&got); err != nil {
		t.Fatal(err)
	}

	want := map[string]any{"type": "dicer", "address": "b:1", "tls": map[string]any{"ca_file": "/ca.pem"}}
	if mustMarshal(t, got) != mustMarshal(t, want) {
		t.Errorf("Resolve() = %v, want %v", got, want)
	}

	// Read on its own, as a provider reads its part, it must still decode:
	// an alias left in would name an anchor that is not there.
	var settings struct {
		Type    string            `yaml:"type"`
		Address string            `yaml:"address"`
		TLS     map[string]string `yaml:"tls"`
	}
	if err := provider.Decode(provider.Resolve(doc.Content[0].Content[1]), &settings); err != nil {
		t.Errorf("Decode() = %v", err)
	}
}
