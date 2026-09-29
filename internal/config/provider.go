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
	"github.com/konradasb/rungar/internal/provider/docker"
	"github.com/konradasb/rungar/internal/provider/gcp"
	"github.com/konradasb/rungar/internal/provider/proxmox"
)

// ProviderConfig is an entry in providers: the keys every provider has, and the
// rest, which its type reads.
type ProviderConfig struct {
	// Name is what scale sets, logs and metrics call the provider.
	Name string `yaml:"name"`

	// Type is the kind of backend: dicer, docker, proxmox, gcp or aws. The
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
	// from. A scale set's block for the provider is written over it: its keys replace
	// the provider's, mappings are merged key by key, and a key set to null
	// is cleared. Its keys are the provider type's.
	RunnerBlock provider.Node `yaml:"runner,omitempty"`

	// raw is the entry less the keys above, for the type to read into
	// settings.
	raw      yaml.Node
	settings provider.Config
}

// Settings returns the provider's settings, as its type read them.
func (p *ProviderConfig) Settings() provider.Config {
	return p.settings
}

// UnmarshalYAML reads the keys every provider has, and keeps the rest for the
// provider's type, with anchors and merge keys resolved.
func (p *ProviderConfig) UnmarshalYAML(node *yaml.Node) error {
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
			return errdefs.InvalidArgument("provider %q: type is required; one of %s", p.Name, knownTypes())
		}
		if _, ok := providerTypes[p.Type]; !ok {
			return errdefs.InvalidArgument("provider %q: unknown type %q; want one of %s",
				p.Name, p.Type, knownTypes())
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

// Provider returns the provider named name.
func (c *Config) Provider(name string) (*ProviderConfig, bool) {
	for i := range c.Providers {
		if c.Providers[i].Name == name {
			return &c.Providers[i], true
		}
	}

	return nil, false
}

// providerTypes maps each provider type's name to the type.
var providerTypes = map[string]provider.Type{
	"aws":     aws.Type{},
	"dicer":   dicer.Type{},
	"docker":  docker.Type{},
	"gcp":     gcp.Type{},
	"proxmox": proxmox.Type{},
}

// knownTypes lists the provider types' names, for a message.
func knownTypes() string {
	return strings.Join(slices.Sorted(maps.Keys(providerTypes)), ", ")
}
