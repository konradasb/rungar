// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package github

import (
	"strings"
	"testing"
)

func TestRESTEndpointOfEachKindOfConfigURL(t *testing.T) {
	tests := []struct {
		url, api, scopePath string
	}{
		{"https://github.com/my-org", "https://api.github.com", "orgs/my-org"},
		{"https://github.com/my-org/my-repo/", "https://api.github.com", "repos/my-org/my-repo"},
		{"https://github.com/enterprises/acme", "https://api.github.com", "enterprises/acme"},
		{"https://www.github.com/my-org", "https://api.github.com", "orgs/my-org"},
		{"https://acme.ghe.com/my-org", "https://api.acme.ghe.com", "orgs/my-org"},
		{"https://git.example.com/my-org/my-repo", "https://git.example.com/api/v3", "repos/my-org/my-repo"},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			api, scopePath, err := restEndpoint(tt.url)
			if err != nil {
				t.Fatalf("restEndpoint() = %v", err)
			}
			if api.String() != tt.api || scopePath != tt.scopePath {
				t.Errorf("restEndpoint() = %s, %s; want %s, %s", api, scopePath, tt.api, tt.scopePath)
			}
		})
	}
}

func TestRESTEndpointRefusesInvalidURL(t *testing.T) {
	for _, bad := range []string{"https://github.com", "https://github.com/a/b/c", "not a url"} {
		t.Run(bad, func(t *testing.T) {
			_, _, err := restEndpoint(bad)
			if err == nil {
				t.Fatal("restEndpoint() = nil, want an error")
			}
			if !strings.Contains(err.Error(), "https://HOST/ORG") {
				t.Errorf("restEndpoint() = %q, want it to say the forms a URL may take", err)
			}
		})
	}
}

func TestScopeOfEachKindOfConfigURL(t *testing.T) {
	tests := []struct {
		url  string
		want Scope
	}{
		{"https://github.com/my-org", ScopeOrganisation},
		{"https://github.com/my-org/my-repo/", ScopeRepository},
		{"https://github.com/enterprises/acme", ScopeEnterprise},
		{"https://git.example.com/Enterprises/acme/", ScopeEnterprise},
		{"https://github.com/enterprises", ScopeOrganisation},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if got, err := ScopeOf(tt.url); err != nil || got != tt.want {
				t.Errorf("ScopeOf() = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestScopeOfRefusesInvalidURL(t *testing.T) {
	for _, bad := range []string{"https://github.com", "https://github.com/a/b/c", "not a url"} {
		t.Run(bad, func(t *testing.T) {
			if _, err := ScopeOf(bad); err == nil {
				t.Error("ScopeOf() = nil, want an error")
			}
		})
	}
}
