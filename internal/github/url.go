// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package github

import (
	"fmt"
	"net/url"
	"strings"
)

// The scopes a configuration URL can name.
const (
	ScopeRepository   = "repository"
	ScopeOrganisation = "organisation"
	ScopeEnterprise   = "enterprise"
)

// Scope returns what a configuration URL names: ScopeRepository,
// ScopeOrganisation or ScopeEnterprise.
func Scope(configURL string) (string, error) {
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
func parseConfigURL(configURL string) (u *url.URL, scope, path string, err error) {
	u, err = url.Parse(strings.Trim(configURL, "/"))
	if err != nil || u.Host == "" {
		return nil, "", "", fmt.Errorf("github: invalid URL %q", configURL)
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
		return nil, "", "", fmt.Errorf("github: %q names no enterprise, organisation or repository", configURL)
	}
}
