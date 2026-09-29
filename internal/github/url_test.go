// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package github

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		url, api, scope string
	}{
		{"https://github.com/my-org", "https://api.github.com", "orgs/my-org"},
		{"https://github.com/my-org/my-repo/", "https://api.github.com", "repos/my-org/my-repo"},
		{"https://github.com/enterprises/acme", "https://api.github.com", "enterprises/acme"},
		{"https://www.github.com/my-org", "https://api.github.com", "orgs/my-org"},
		{"https://acme.ghe.com/my-org", "https://api.acme.ghe.com", "orgs/my-org"},
		{"https://git.example.com/my-org/my-repo", "https://git.example.com/api/v3", "repos/my-org/my-repo"},
	}

	for _, tt := range tests {
		api, scope, err := restEndpoint(tt.url)
		if err != nil {
			t.Errorf("parse(%q) = %v", tt.url, err)
			continue
		}
		if api.String() != tt.api || scope != tt.scope {
			t.Errorf("parse(%q) = %s, %s; want %s, %s", tt.url, api, scope, tt.api, tt.scope)
		}
	}

	for _, bad := range []string{"https://github.com", "https://github.com/a/b/c", "not a url"} {
		if _, _, err := restEndpoint(bad); err == nil {
			t.Errorf("parse(%q) = nil, want an error", bad)
		}
	}
}

func TestScope(t *testing.T) {
	tests := []struct {
		url, want string
	}{
		{"https://github.com/my-org", ScopeOrganisation},
		{"https://github.com/my-org/my-repo/", ScopeRepository},
		{"https://github.com/enterprises/acme", ScopeEnterprise},
		{"https://git.example.com/Enterprises/acme/", ScopeEnterprise},
		{"https://github.com/enterprises", ScopeOrganisation},
	}

	for _, tt := range tests {
		if got, err := Scope(tt.url); err != nil || got != tt.want {
			t.Errorf("Scope(%q) = %q, %v; want %q", tt.url, got, err, tt.want)
		}
	}

	for _, bad := range []string{"https://github.com", "https://github.com/a/b/c", "not a url"} {
		if _, err := Scope(bad); err == nil {
			t.Errorf("Scope(%q) = nil, want an error", bad)
		}
	}
}
