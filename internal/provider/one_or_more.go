// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package provider

import (
	"encoding/json"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/konradasb/rungar/internal/errdefs"
)

// OneOrMore are strings tried in order, such as a runner's instance types:
// one, written as a string, or several, as a list. One is written back as a
// string, to YAML and to JSON alike, so that a runner's revision is what it
// was before a list could be given.
type OneOrMore []string

// UnmarshalYAML reads a string, or a list of them.
func (o *OneOrMore) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var one string
		if err := node.Decode(&one); err != nil {
			return err
		}
		*o = OneOrMore{one}
	case yaml.SequenceNode:
		var several []string
		if err := node.Decode(&several); err != nil {
			return err
		}
		*o = several
	default:
		return errdefs.InvalidArgument("want a string, or a list of strings")
	}

	return nil
}

// MarshalYAML writes one string as a string, and several as a list.
func (o OneOrMore) MarshalYAML() (any, error) {
	if len(o) == 1 {
		return o[0], nil
	}

	return []string(o), nil
}

// MarshalJSON writes one string, or none, as a string, and several as a list.
func (o OneOrMore) MarshalJSON() ([]byte, error) {
	switch len(o) {
	case 0:
		return json.Marshal("")
	case 1:
		return json.Marshal(o[0])
	default:
		return json.Marshal([]string(o))
	}
}

// String returns the strings as a description shows them: "a or b".
func (o OneOrMore) String() string {
	return strings.Join(o, " or ")
}
