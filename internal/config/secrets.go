// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package config

import (
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/konradasb/rungar/internal/provider"
)

// secretKeysNamer is a provider.Config that can hold a secret inline, naming
// the keys it is under, which YAML redacts.
type secretKeysNamer interface {
	SecretKeys() []string
}

// secretKeysOf returns the keys of a provider's config that hold a secret
// inline: none, unless its type names them.
func secretKeysOf(cfg provider.Config) []string {
	if namer, ok := cfg.(secretKeysNamer); ok {
		return namer.SecretKeys()
	}

	return nil
}

// secretFilesNamer is a provider.Config that names the files holding its
// secrets, which the daemon warns of at start if anyone can read them.
type secretFilesNamer interface {
	SecretFiles() []string
}

// SecretFiles returns the files holding a secret: the GitHub credential, each
// provider's keys, and the configuration file itself when the GitHub
// credential or a provider's secret is inline.
func (c *Config) SecretFiles() []string {
	var files []string
	for _, f := range []string{c.GitHub.TokenPath, c.GitHub.AppPrivateKeyPath} {
		if f != "" {
			files = append(files, f)
		}
	}

	inline := c.GitHub.Token != "" || c.GitHub.AppPrivateKey != ""
	for _, p := range c.Providers {
		if namer, ok := p.config.(secretFilesNamer); ok {
			files = append(files, namer.SecretFiles()...)
		}
		inline = inline || p.hasInlineSecret()
	}

	if c.path != "" && inline {
		files = append(files, c.path)
	}

	return files
}

// hasInlineSecret reports whether the provider's entry sets one of the keys
// its type holds a secret under.
func (p *Provider) hasInlineSecret() bool {
	secretKeys := secretKeysOf(p.config)
	for i := 0; i+1 < len(p.raw.Content); i += 2 {
		if isInlineSecret(secretKeys, p.raw.Content[i], p.raw.Content[i+1]) {
			return true
		}
	}

	return false
}

// isInlineSecret reports whether a key and its value in a provider's entry set
// one of secretKeys, the keys its type holds a secret under.
func isInlineSecret(secretKeys []string, key, value *yaml.Node) bool {
	return slices.Contains(secretKeys, key.Value) && value.Value != ""
}
