// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package config

import (
	"maps"
	"math"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/provider/aws"
	"github.com/konradasb/rungar/internal/provider/dicer"
	"github.com/konradasb/rungar/internal/provider/gcp"
	"github.com/konradasb/rungar/internal/provider/proxmox"
)

// Provider is an entry in providers: the keys every provider has, and the
// rest, which its type reads.
type Provider struct {
	// Name is what scale sets, logs and metrics call the provider.
	Name string `yaml:"name"`

	// Type is the kind of backend: dicer, proxmox, gcp or aws. The
	// rest of the entry is the type's; see its reference.
	Type string `yaml:"type"`

	// Weight scales how attractive the provider is to placement: spread
	// counts one of weight 2 as having half the runners it has, and pack
	// tries the highest weights first. Unset is 1.
	Weight float64 `yaml:"weight,omitempty"`

	// MaxRunners is the most runners the provider may have, of every scale
	// set placed on it: a ceiling of your own, such as a cloud's cost, where
	// the backend would take more. A provider at it is not tried. Unset
	// leaves it to the backend, which refuses a runner once it is full. It
	// may not be negative.
	MaxRunners int `yaml:"max_runners,omitempty"`

	// Disabled takes the provider out of placement, leaving the runners on
	// it to finish their jobs.
	Disabled bool `yaml:"disabled,omitempty"`

	// RunnerBlock is the runner block every scale set on the provider starts
	// from. A scale set's block for the provider is written over it: its keys
	// replace the provider's, mappings are merged key by key, and a key set to
	// null is cleared. Its keys are the provider type's.
	RunnerBlock provider.Node `yaml:"runner,omitempty"`

	// raw is the entry less the keys above, for the type to read into
	// config.
	raw    yaml.Node
	config provider.Config
}

// Config returns the provider's own settings, as its type read them.
func (p *Provider) Config() provider.Config {
	return p.config
}

// UnmarshalYAML reads the keys every provider has, and keeps the rest for the
// provider's type, with anchors and merge keys resolved.
func (p *Provider) UnmarshalYAML(node *yaml.Node) error {
	node = provider.Resolve(node)
	if node.Kind != yaml.MappingNode {
		return errdefs.InvalidArgument("line %d: a provider is a mapping, with a name and a type", node.Line)
	}

	p.raw = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Line: node.Line, Column: node.Column}

	common := map[string]any{
		"name":        &p.Name,
		"type":        &p.Type,
		"weight":      &p.Weight,
		"max_runners": &p.MaxRunners,
		"disabled":    &p.Disabled,
	}

	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]

		if dst, ok := common[key.Value]; ok {
			if err := value.Decode(dst); err != nil {
				return err
			}
			continue
		}

		if key.Value != "runner" {
			p.raw.Content = append(p.raw.Content, key, value)
			continue
		}
		if value.Kind != yaml.MappingNode && value.Tag != "!!null" {
			return errdefs.InvalidArgument("line %d: runner is a mapping", value.Line)
		}
		p.RunnerBlock = *value
	}

	return nil
}

// MarshalYAML writes the keys every provider has, then the rest as its type
// read them, defaults and all, with inline secrets redacted. Its receiver is a
// value, as the encoder looks for a Marshaler on the element of a slice
// without taking its address.
func (p Provider) MarshalYAML() (any, error) {
	entry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}

	add := func(key string, value any) error {
		var v yaml.Node
		if err := v.Encode(value); err != nil {
			return err
		}
		entry.Content = append(entry.Content, scalarNode(key), &v)

		return nil
	}

	common := []struct {
		key   string
		value any
		set   bool
	}{
		{"name", p.Name, true},
		{"type", p.Type, true},
		{"weight", p.Weight, true},
		{"max_runners", p.MaxRunners, p.MaxRunners != 0},
		{"disabled", p.Disabled, p.Disabled},
	}
	for _, c := range common {
		if !c.set {
			continue
		}
		if err := add(c.key, c.value); err != nil {
			return nil, err
		}
	}

	// The settings as the type read them, or as written if it has not.
	settings := p.raw
	if p.config != nil {
		settings = yaml.Node{}
		if err := settings.Encode(p.config); err != nil {
			return nil, err
		}
	}
	secretKeys := secretKeysOf(p.config)
	for i := 0; i+1 < len(settings.Content); i += 2 {
		key, value := settings.Content[i], settings.Content[i+1]
		if isInlineSecret(secretKeys, key, value) {
			value = scalarNode(redacted)
		}
		entry.Content = append(entry.Content, key, value)
	}

	if !isEmptyNode(&p.RunnerBlock) {
		entry.Content = append(entry.Content, scalarNode("runner"), printable(&p.RunnerBlock))
	}

	return entry, nil
}

// validateProviders checks every provider has a unique name, a known type and
// valid common keys. The rest of each entry is its type's to check; see
// resolve.
func (c *Config) validateProviders() error {
	if len(c.Providers) == 0 {
		return errdefs.InvalidArgument("no providers: Rungar needs at least one to place runners on")
	}

	seen := make(map[string]bool, len(c.Providers))
	for _, p := range c.Providers {
		switch {
		case p.Name == "":
			return errdefs.InvalidArgument("a provider needs a name, which scale sets name it by")
		case seen[p.Name]:
			return errdefs.InvalidArgument("two providers are both named %q", p.Name)
		case p.Type == "":
			return errdefs.InvalidArgument("provider %q: type is required; one of %s",
				p.Name, knownProviderTypes())
		}
		if _, ok := providerTypes[p.Type]; !ok {
			return errdefs.InvalidArgument("provider %q: unknown type %q; want one of %s",
				p.Name, p.Type, knownProviderTypes())
		}
		if !(p.Weight > 0) || math.IsInf(p.Weight, 1) {
			return errdefs.InvalidArgument("provider %q: weight must be positive and finite", p.Name)
		}
		if p.MaxRunners < 0 {
			return errdefs.InvalidArgument("provider %q: max_runners cannot be negative; leave it unset for no limit",
				p.Name)
		}
		seen[p.Name] = true
	}

	return nil
}

// Provider returns the provider named name, or false if there is none.
func (c *Config) Provider(name string) (*Provider, bool) {
	for i := range c.Providers {
		if c.Providers[i].Name == name {
			return &c.Providers[i], true
		}
	}

	return nil, false
}

// providerTypes maps each provider type's name to the type, whose package is
// internal/provider/<name>. Type's doc comment, which the configuration
// reference is generated from, lists them too.
var providerTypes = map[string]provider.Type{
	"aws":     aws.Type{},
	"dicer":   dicer.Type{},
	"gcp":     gcp.Type{},
	"proxmox": proxmox.Type{},
}

// ProviderTypes returns the names of the provider types, sorted.
func ProviderTypes() []string {
	return slices.Sorted(maps.Keys(providerTypes))
}

// knownProviderTypes lists the provider types' names, for a message.
func knownProviderTypes() string {
	return strings.Join(ProviderTypes(), ", ")
}
