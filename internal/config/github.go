// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/golang-jwt/jwt/v4"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/github"
)

// GitHubConfig is which GitHub to serve, and the credential: a GitHub App,
// preferably, or a personal access token.
//
// A credential is given inline or as the path of a file holding it. A path
// keeps the secret out of the configuration and lets it be rotated without an
// edit; inline makes the configuration file itself a secret.
type GitHubConfig struct {
	// URL is the enterprise, organisation or repository the scale sets
	// belong to: "https://github.com/my-org", or a GitHub Enterprise Server
	// URL. An enterprise needs a token: GitHub does not let an App manage
	// an enterprise's runners.
	URL string `yaml:"url"`

	// AppClientID is the App's client ID, which looks like "Iv1.abc123".
	AppClientID string `yaml:"app_client_id,omitempty"`

	// AppInstallationID is the App's installation on the organisation or
	// repository.
	AppInstallationID int64 `yaml:"app_installation_id,omitempty"`

	// AppPrivateKey is the App's private key in PEM, and AppPrivateKeyPath
	// a file holding it. Give one or the other.
	AppPrivateKey     string `yaml:"app_private_key,omitempty"`
	AppPrivateKeyPath string `yaml:"app_private_key_path,omitempty"`

	// Token is a personal access token, and TokenPath a file holding one.
	// Give one or the other, and neither with an App.
	Token     string `yaml:"token,omitempty"`
	TokenPath string `yaml:"token_path,omitempty"`
}

// UsesApp reports whether anything about a GitHub App is configured.
func (g *GitHubConfig) UsesApp() bool {
	return g.AppClientID != "" || g.AppInstallationID != 0 ||
		g.AppPrivateKey != "" || g.AppPrivateKeyPath != ""
}

// ReadToken returns the personal access token, inline or from its file, or ""
// when none is configured.
func (g *GitHubConfig) ReadToken() (string, error) {
	if g.Token != "" {
		return strings.TrimSpace(g.Token), nil
	}
	if g.TokenPath == "" {
		return "", nil
	}

	b, err := os.ReadFile(g.TokenPath)
	if err != nil {
		return "", fmt.Errorf("read github token: %w", err)
	}

	return strings.TrimSpace(string(b)), nil
}

// ReadPrivateKey returns the App's private key, inline or from its file.
func (g *GitHubConfig) ReadPrivateKey() (string, error) {
	if g.AppPrivateKey != "" {
		return g.AppPrivateKey, nil
	}

	b, err := os.ReadFile(g.AppPrivateKeyPath)
	if err != nil {
		return "", fmt.Errorf("read github app private key: %w", err)
	}

	return string(b), nil
}

// CheckFiles reads the credential, and parses an App's private key.
func (g *GitHubConfig) CheckFiles() error {
	if !g.UsesApp() {
		token, err := g.ReadToken()
		if err != nil {
			return err
		}
		if token == "" {
			return errdefs.InvalidArgument("github: the token is empty")
		}

		return nil
	}

	key, err := g.ReadPrivateKey()
	if err != nil {
		return err
	}
	if _, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(key)); err != nil {
		return errdefs.InvalidArgument("github: the App's private key: %s", err)
	}

	return nil
}

// Summary describes the credential without revealing it: an App by its client
// and installation, a token by where it is read from.
func (g *GitHubConfig) Summary() string {
	if g.UsesApp() {
		return fmt.Sprintf("GitHub App %s, installation %d, key %s",
			g.AppClientID, g.AppInstallationID, source(g.AppPrivateKeyPath))
	}

	return "GitHub PAT (" + source(g.TokenPath) + ")"
}

// source describes where a credential is read from.
func source(path string) string {
	if path == "" {
		return "given inline"
	}

	return "source: " + path
}

// scopeUnrecognised is the scope of a URL naming no enterprise, organisation or
// repository.
const scopeUnrecognised = "unrecognised"

// Scope returns what the URL names: a repository, an organisation or an
// enterprise.
func (g *GitHubConfig) Scope() string {
	scope, err := github.Scope(g.URL)
	if err != nil {
		return scopeUnrecognised
	}

	return scope
}

func (g *GitHubConfig) validate() error {
	if g.URL == "" {
		return errdefs.InvalidArgument("github.url is required: the enterprise, organisation or " +
			"repository the scale sets belong to")
	}
	if _, err := github.Scope(g.URL); err != nil {
		return errdefs.InvalidArgument("invalid github.url %q: want https://HOST/ORG, https://HOST/OWNER/REPO "+
			"or https://HOST/enterprises/ENTERPRISE", g.URL)
	}

	if g.UsesApp() {
		return g.validateApp()
	}

	switch {
	case g.Token != "" && g.TokenPath != "":
		return errdefs.InvalidArgument("github: give either token or token_path, not both")
	case g.Token == "" && g.TokenPath == "":
		return errdefs.InvalidArgument("github: no credentials; configure a GitHub App " +
			"(recommended) or a token")
	}

	return nil
}

// validateApp checks a GitHub App is fully configured, alone, and not for an
// enterprise.
func (g *GitHubConfig) validateApp() error {
	switch {
	case g.AppClientID == "":
		return errdefs.InvalidArgument("github.app_client_id is required for a GitHub App")
	case g.AppInstallationID == 0:
		return errdefs.InvalidArgument("github.app_installation_id is required for a GitHub App")
	case g.AppPrivateKey != "" && g.AppPrivateKeyPath != "":
		return errdefs.InvalidArgument(
			"github: give either app_private_key or app_private_key_path, not both")
	case g.AppPrivateKey == "" && g.AppPrivateKeyPath == "":
		return errdefs.InvalidArgument(
			"github: a GitHub App needs app_private_key or app_private_key_path")
	case g.Token != "" || g.TokenPath != "":
		return errdefs.InvalidArgument("github: configure either a GitHub App or a token, not both")
	case g.Scope() == github.ScopeEnterprise:
		return errdefs.InvalidArgument("github: %s is an enterprise, and GitHub does not allow a GitHub "+
			"App to manage an enterprise's runners; use a classic token with manage_runners:enterprise", g.URL)
	}

	return nil
}
