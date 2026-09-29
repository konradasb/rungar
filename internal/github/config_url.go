// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package github

import (
	"fmt"
	"net/url"
	"strings"
)

// Scope is what a configuration URL names: a repository, an organisation or
// an enterprise.
type Scope string

// The scopes a configuration URL can name.
const (
	ScopeRepository   Scope = "repository"
	ScopeOrganisation Scope = "organisation"
	ScopeEnterprise   Scope = "enterprise"
)

// ScopeOf returns what a configuration URL names.
func ScopeOf(configURL string) (Scope, error) {
	_, scope, _, err := parseConfigURL(configURL)
	return scope, err
}

// restEndpoint returns the REST API's base URL and the API path of a
// configuration URL's scope: repos/OWNER/REPO, orgs/ORG or
// enterprises/ENTERPRISE.
func restEndpoint(configURL string) (*url.URL, string, error) {
	u, _, path, err := parseConfigURL(configURL)
	if err != nil {
		return nil, "", err
	}

	// github.com and GHE.com serve the API on its own host; GitHub
	// Enterprise Server serves it under /api/v3.
	api := &url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/api/v3"}

	host := strings.ToLower(u.Host)
	if host == "github.com" || host == "www.github.com" || strings.HasSuffix(host, ".ghe.com") {
		api.Host = "api." + strings.TrimPrefix(host, "www.")
		api.Path = ""
	}

	return api, path, nil
}

// parseConfigURL parses a configuration URL, and returns its scope and the
// scope's REST API path.
func parseConfigURL(configURL string) (u *url.URL, scope Scope, path string, err error) {
	u, err = url.Parse(strings.Trim(configURL, "/"))
	if err != nil || u.Host == "" {
		return nil, "", "", fmt.Errorf("github: invalid URL %q: %s", configURL, configURLForms)
	}

	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] != "":
		return u, ScopeOrganisation, "orgs/" + parts[0], nil
	case len(parts) == 2 && strings.EqualFold(parts[0], "enterprises"):
		return u, ScopeEnterprise, "enterprises/" + parts[1], nil
	case len(parts) == 2:
		return u, ScopeRepository, "repos/" + parts[0] + "/" + parts[1], nil
	default:
		return nil, "", "", fmt.Errorf("github: %q names no enterprise, organisation or repository: %s",
			configURL, configURLForms)
	}
}

// configURLForms says what a configuration URL may look like, for errors.
const configURLForms = "want https://HOST/ORG, https://HOST/OWNER/REPO or https://HOST/enterprises/NAME"
