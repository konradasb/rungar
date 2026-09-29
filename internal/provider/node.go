// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package provider

import (
	"bytes"
	"errors"
	"io"

	"gopkg.in/yaml.v3"

	"github.com/konradasb/rungar/internal/errdefs"
)

// Node is the part of the configuration a provider reads: its entry, or a
// runner block.
type Node = yaml.Node

// Decode reads a node into v, refusing keys v does not have. A nil or empty
// node leaves v as it is.
func Decode(node *Node, v any) error {
	if node == nil || node.Kind == 0 {
		return nil
	}

	b, err := yaml.Marshal(node)
	if err != nil {
		return err
	}

	decoder := yaml.NewDecoder(bytes.NewReader(b))
	decoder.KnownFields(true)

	if err := decoder.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return errdefs.InvalidArgument("%s", err)
	}

	return nil
}

// Merge returns over written on top of base, as a scale set's runner block for
// a provider is over the provider's. Mappings are merged key by key at every depth;
// anything else in over, lists included, replaces base's, and a key set to
// null is cleared. Aliases and merge keys are resolved first (see Resolve),
// and neither node is changed.
func Merge(base, over *Node) *Node {
	base, over = Resolve(base), Resolve(over)

	switch {
	case isEmpty(over):
		return base
	case isEmpty(base):
		return over
	case base.Kind != yaml.MappingNode || over.Kind != yaml.MappingNode:
		return over
	}

	for i := 0; i+1 < len(over.Content); i += 2 {
		key, value := over.Content[i], over.Content[i+1]

		j := indexOf(base, key.Value)
		switch {
		case j < 0:
			base.Content = append(base.Content, key, value)
		case base.Content[j+1].Kind == yaml.MappingNode && value.Kind == yaml.MappingNode:
			base.Content[j+1] = Merge(base.Content[j+1], value)
		default:
			base.Content[j+1] = value
		}
	}

	return base
}

// Resolve returns a copy of n that can be read apart from its document: each
// alias is replaced by what it names, and each merge key (<<) by the keys it
// brings that the mapping does not set itself. It lets providers share
// settings through anchors:
//
//	providers:
//	  - &dicer {name: compute1, type: dicer, address: 10.10.0.101:7443}
//	  - {<<: *dicer, name: compute2, address: 10.10.0.102:7443}
func Resolve(n *Node) *Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.AliasNode {
		return Resolve(n.Alias)
	}

	c := *n
	c.Anchor = ""
	c.Content = nil

	if n.Kind != yaml.MappingNode {
		for _, child := range n.Content {
			c.Content = append(c.Content, Resolve(child))
		}

		return &c
	}

	var merged []*Node

	for i := 0; i+1 < len(n.Content); i += 2 {
		key, value := n.Content[i], Resolve(n.Content[i+1])

		if !isMergeKey(key) {
			c.Content = append(c.Content, Resolve(key), value)
			continue
		}

		switch value.Kind {
		case yaml.MappingNode:
			merged = append(merged, value)
		case yaml.SequenceNode:
			merged = append(merged, value.Content...)
		}
	}

	for _, m := range merged {
		for i := 0; i+1 < len(m.Content); i += 2 {
			if indexOf(&c, m.Content[i].Value) < 0 {
				c.Content = append(c.Content, m.Content[i], m.Content[i+1])
			}
		}
	}

	return &c
}

// isMergeKey reports whether a mapping's key is YAML's merge key.
func isMergeKey(key *Node) bool {
	return key.Kind == yaml.ScalarNode && (key.Tag == "!!merge" || (key.Tag == "" && key.Value == "<<"))
}

// isEmpty reports whether a node is absent or an empty mapping.
func isEmpty(n *Node) bool {
	return n == nil || n.Kind == 0 || (n.Kind == yaml.MappingNode && len(n.Content) == 0)
}

// indexOf returns where a mapping's key is in its Content, or -1.
func indexOf(mapping *Node, key string) int {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return i
		}
	}

	return -1
}
