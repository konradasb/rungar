// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package config

import (
	"bytes"

	"gopkg.in/yaml.v3"

	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

// redacted stands for an inline secret in the configuration YAML prints.
const redacted = "REDACTED"

// YAML returns the configuration as the daemon would run it: its defaults and
// each provider type's filled in, anchors and merge keys resolved, and inline
// secrets redacted. Runner blocks are as written, not merged: a scale set's
// runner on a provider is the provider's block with the scale set's over it.
func (c *Config) YAML() ([]byte, error) {
	out := *c

	if out.GitHub.Token != "" {
		out.GitHub.Token = redacted
	}
	if out.GitHub.AppPrivateKey != "" {
		out.GitHub.AppPrivateKey = redacted
	}

	out.ScaleSets = make([]types.ScaleSetSpec, len(c.ScaleSets))
	for i, set := range c.ScaleSets {
		set.Providers = append(set.Providers[:0:0], set.Providers...)
		for j := range set.Providers {
			if block := &set.Providers[j].RunnerBlock; !isEmptyNode(block) {
				*block = *printable(block)
			}
		}
		out.ScaleSets[i] = set
	}

	return encodeYAML(&out)
}

// encodeYAML encodes v as YAML, indented by two spaces.
func encodeYAML(v any) ([]byte, error) {
	var buf bytes.Buffer

	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)

	if err := encoder.Encode(v); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// printable returns a copy of a runner block with its anchors and merge keys
// resolved and its comments dropped, as the rest of the output has none.
func printable(n *yaml.Node) *yaml.Node {
	n = provider.Resolve(n)
	stripComments(n)

	return n
}

// stripComments removes the comments of a node and of everything under it.
func stripComments(n *yaml.Node) {
	n.HeadComment, n.LineComment, n.FootComment = "", "", ""
	for _, child := range n.Content {
		stripComments(child)
	}
}

// scalarNode returns a string as a node.
func scalarNode(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

// isEmptyNode reports whether a node is absent or an empty mapping.
func isEmptyNode(n *yaml.Node) bool {
	return n.Kind == 0 || (n.Kind == yaml.MappingNode && len(n.Content) == 0)
}
