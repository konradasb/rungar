// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package provider_test

import (
	"errors"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
)

// node parses YAML into the node a provider type is handed.
func node(t *testing.T, body string) *provider.Node {
	t.Helper()

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}

	return doc.Content[0]
}

// settings is a provider's part of the configuration, for Decode.
type settings struct {
	Address string `yaml:"address"`
}

func TestDecodeFillsInTheSettings(t *testing.T) {
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
// error, not a setting silently left at its default. The error still holds
// YAML's.
func TestDecodeRefusesWhatItDoesNotKnow(t *testing.T) {
	var s settings
	err := provider.Decode(node(t, "addr: 10.0.0.1:7443\n"), &s)
	if !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("Decode() = %v, want an ErrInvalidArgument", err)
	}

	var typeErr *yaml.TypeError
	if !errors.As(err, &typeErr) {
		t.Errorf("Decode() = %v, want it to wrap a *yaml.TypeError", err)
	}
}

func TestDecodeOfNothingChangesNothing(t *testing.T) {
	s := settings{Address: "kept"}
	if err := provider.Decode(nil, &s); err != nil || s.Address != "kept" {
		t.Errorf("Decode(nil) = %v, left %+v; want nothing changed", err, s)
	}
	if err := provider.Decode(&provider.Node{}, &s); err != nil || s.Address != "kept" {
		t.Errorf("Decode(empty) = %v, left %+v; want nothing changed", err, s)
	}
}

// TestMergeWritesTheScaleSetsBlockOverTheProviders checks how a scale set's
// runner block is written over its provider's, which is the same for every
// type of provider.
func TestMergeWritesTheScaleSetsBlockOverTheProviders(t *testing.T) {
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

func TestMergeOfNothingIsTheOtherBlock(t *testing.T) {
	base := node(t, "vcpus: 2\n")

	for name, got := range map[string]*provider.Node{
		"no scale set block": provider.Merge(base, &provider.Node{}),
		"no provider block":  provider.Merge(&provider.Node{}, base),
		"an empty block":     provider.Merge(base, node(t, "{}")),
	} {
		t.Run(name, func(t *testing.T) {
			if s := mustMarshal(t, got); s != "vcpus: 2\n" {
				t.Errorf("Merge() = %q, want the other block", s)
			}
		})
	}

	t.Run("neither block", func(t *testing.T) {
		if got := provider.Merge(nil, nil); got != nil {
			t.Errorf("Merge(nil, nil) = %v, want nil", got)
		}
	})
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()

	b, err := yaml.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

// TestResolveWritesOutAnchorsAndMergeKeys checks settings shared with an
// anchor and a merge key are read as if written out: the entry's own keys
// winning, and nothing left pointing at the document the anchor was in.
func TestResolveWritesOutAnchorsAndMergeKeys(t *testing.T) {
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

// nodeSeeds are YAML documents for the fuzz tests to start from, with the
// anchors, aliases and merge keys that Resolve replaces.
var nodeSeeds = []string{
	"{type: dicer, address: a:1}",
	"- &dicer {type: dicer, address: a:1, tls: {ca_file: /ca.pem}}\n- {<<: *dicer, address: b:1}\n",
	"a: &a {x: 1}\nb: &b {y: 2}\nc: {<<: [*a, *b], z: 3}\n",
	"a: &a [1, 2]\nb: *a\n",
	"image: x\nmounts: [{source: s, target: /t}]\nvcpus: 2\n",
	"memory: null\n",
	"",
}

// FuzzResolve checks that a resolved node has no aliases or merge keys left,
// and reads as the document it came from.
func FuzzResolve(f *testing.F) {
	for _, seed := range nodeSeeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, b []byte) {
		var doc yaml.Node
		if yaml.Unmarshal(b, &doc) != nil || len(doc.Content) == 0 || !stringKeyed(&doc) {
			return
		}

		var want any
		if doc.Content[0].Decode(&want) != nil {
			// Refused by yaml.v3 itself: an alias expanded too often, say.
			return
		}

		resolved := provider.Resolve(doc.Content[0])
		if n := unresolved(resolved); n != nil {
			t.Fatalf("Resolve() left %v (%q) in\n%s", n.Kind, n.Value, b)
		}

		var got any
		if err := resolved.Decode(&got); err != nil {
			t.Fatalf("the resolved node does not decode: %v\n%s", err, b)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("resolved, %s reads as %#v, want %#v", b, got, want)
		}
	})
}

// stringKeyed reports whether every mapping key in n is a string, as in a
// configuration. yaml.v3 reads another key, such as 00, as a number in a
// mapping but as a string in one it merges into.
func stringKeyed(n *yaml.Node) bool {
	for i, child := range n.Content {
		if n.Kind == yaml.MappingNode && i%2 == 0 && child.Tag != "!!str" && child.Tag != "!!merge" {
			return false
		}
		if !stringKeyed(child) {
			return false
		}
	}

	return true
}

// decodable reports whether yaml.v3 decodes b into a value, as it refuses an
// anchor containing an alias to itself, which Resolve requires.
func decodable(b []byte) bool {
	var v any
	return yaml.Unmarshal(b, &v) == nil
}

// unresolved returns an alias or merge key in n, or nil if there is none.
func unresolved(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.AliasNode {
		return n
	}
	for i, child := range n.Content {
		if n.Kind == yaml.MappingNode && i%2 == 0 && child.Tag == "!!merge" {
			return child
		}
		if u := unresolved(child); u != nil {
			return u
		}
	}

	return nil
}

// FuzzMerge checks that merging two runner blocks changes neither.
func FuzzMerge(f *testing.F) {
	for _, base := range nodeSeeds {
		f.Add([]byte(base), []byte("{image: y, vcpus: 4}"))
	}

	f.Fuzz(func(t *testing.T, base, over []byte) {
		var baseDoc, overDoc yaml.Node
		if !decodable(base) || !decodable(over) ||
			yaml.Unmarshal(base, &baseDoc) != nil || yaml.Unmarshal(over, &overDoc) != nil {
			return
		}

		baseBefore, overBefore := mustMarshal(t, &baseDoc), mustMarshal(t, &overDoc)

		var baseNode, overNode *yaml.Node
		if len(baseDoc.Content) > 0 {
			baseNode = baseDoc.Content[0]
		}
		if len(overDoc.Content) > 0 {
			overNode = overDoc.Content[0]
		}

		provider.Merge(baseNode, overNode)

		if got := mustMarshal(t, &baseDoc); got != baseBefore {
			t.Errorf("Merge() changed base:\n%s\nto\n%s", baseBefore, got)
		}
		if got := mustMarshal(t, &overDoc); got != overBefore {
			t.Errorf("Merge() changed over:\n%s\nto\n%s", overBefore, got)
		}
	})
}
