// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konradasb/rungar/internal/errdefs"
)

func TestBothCredentialsRefused(t *testing.T) {
	body := strings.Replace(valid,
		"  app_private_key_path: /etc/rungar/app.pem\n",
		"  app_private_key_path: /etc/rungar/app.pem\n  token_path: /etc/rungar/token\n", 1)

	if _, err := Load(writeConfig(t, body)); err == nil {
		t.Fatal("Load() = nil, want an error: an app and a token is ambiguous")
	}
}

func TestTokenIsTrimmed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("  ghp_secret\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	github := GitHubConfig{TokenPath: path}

	got, err := github.ReadToken()
	if err != nil {
		t.Fatalf("ReadToken() = %v", err)
	}

	// A trailing newline in the file would otherwise travel into an HTTP
	// header, where GitHub rejects it for reasons that say nothing useful.
	if got != "ghp_secret" {
		t.Errorf("ReadToken() = %q, want it trimmed", got)
	}
}

func TestTokenIsEmptyWithoutAPath(t *testing.T) {
	got, err := (&GitHubConfig{}).ReadToken()
	if err != nil {
		t.Fatalf("ReadToken() = %v", err)
	}
	if got != "" {
		t.Errorf("ReadToken() = %q, want empty when an app is configured instead", got)
	}
}

func TestPrivateKeyIsRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.pem")
	if err := os.WriteFile(path, []byte("-----BEGIN-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := (&GitHubConfig{AppPrivateKeyPath: path}).ReadPrivateKey()
	if err != nil {
		t.Fatalf("ReadPrivateKey() = %v", err)
	}
	if got == "" {
		t.Error("ReadPrivateKey() returned nothing")
	}

	if _, err := (&GitHubConfig{AppPrivateKeyPath: "/nonexistent"}).ReadPrivateKey(); err == nil {
		t.Error("ReadPrivateKey() = nil for a key that is not there")
	}
}

// githubBody returns a configuration whose github block is the given body, for
// tests about credentials alone.
func githubBody(body string) string {
	return `
github:
` + body + `
providers:
  - name: local
    type: dicer
    address: unix:///run/dicer/dicer.sock
scale_sets:
  - name: rungar-vm
    max_runners: 4
    providers:
      - name: local
        runner: {image: "runner:latest", vcpus: 2, memory: 4GiB}
`
}

func TestCredentialsAccepted(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "an app with its key in a file",
			body: "  url: https://github.com/org\n  app_client_id: Iv1.abc\n" +
				"  app_installation_id: 42\n  app_private_key_path: /etc/rungar/app.pem\n",
		},
		{
			name: "an app with its key inline",
			body: "  url: https://github.com/org\n  app_client_id: Iv1.abc\n" +
				"  app_installation_id: 42\n  app_private_key: |\n    -----BEGIN KEY-----\n",
		},
		{
			name: "a token in a file",
			body: "  url: https://github.com/org\n  token_path: /etc/rungar/token\n",
		},
		{
			name: "a token inline",
			body: "  url: https://github.com/org\n  token: ghp_secret\n",
		},
		{
			name: "a token for an enterprise",
			body: "  url: https://github.com/enterprises/my-enterprise\n  token: ghp_secret\n",
		},
		{
			name: "an app for a repository of an owner named enterprises",
			body: "  url: https://github.com/enterprises\n  app_client_id: Iv1.abc\n" +
				"  app_installation_id: 42\n  app_private_key_path: /etc/rungar/app.pem\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, githubBody(tt.body))); err != nil {
				t.Errorf("Load() = %v", err)
			}
		})
	}
}

func TestCredentialsRefused(t *testing.T) {
	tests := []struct {
		name string
		body string
		says string
	}{
		{
			name: "nothing at all",
			body: "  url: https://github.com/org\n",
			says: "no credentials",
		},
		{
			name: "a URL naming no organisation",
			body: "  url: https://github.com\n  token: ghp_secret\n",
			says: "invalid github.url",
		},
		{
			name: "a URL naming too much",
			body: "  url: https://github.com/org/repo/tree\n  token: ghp_secret\n",
			says: "invalid github.url",
		},
		{
			name: "an app with no client",
			body: "  url: https://github.com/org\n  app_installation_id: 42\n" +
				"  app_private_key_path: /etc/rungar/app.pem\n",
			says: "app_client_id",
		},
		{
			name: "an app with no installation",
			body: "  url: https://github.com/org\n  app_client_id: Iv1.abc\n" +
				"  app_private_key_path: /etc/rungar/app.pem\n",
			says: "app_installation_id",
		},
		{
			name: "an app with no key",
			body: "  url: https://github.com/org\n  app_client_id: Iv1.abc\n  app_installation_id: 42\n",
			says: "app_private_key",
		},
		{
			name: "a key given twice",
			body: "  url: https://github.com/org\n  app_client_id: Iv1.abc\n  app_installation_id: 42\n" +
				"  app_private_key: x\n  app_private_key_path: /etc/rungar/app.pem\n",
			says: "not both",
		},
		{
			name: "a token given twice",
			body: "  url: https://github.com/org\n  token: x\n  token_path: /etc/rungar/token\n",
			says: "not both",
		},
		{
			name: "an app and a token together",
			body: "  url: https://github.com/org\n  app_client_id: Iv1.abc\n  app_installation_id: 42\n" +
				"  app_private_key: x\n  token: ghp_secret\n",
			says: "not both",
		},
		{
			name: "an app for an enterprise",
			body: "  url: https://github.com/enterprises/my-enterprise\n  app_client_id: Iv1.abc\n" +
				"  app_installation_id: 42\n  app_private_key_path: /etc/rungar/app.pem\n",
			says: "does not allow a GitHub App",
		},
		{
			name: "an app for an enterprise on GitHub Enterprise Server",
			body: "  url: https://github.example.com/Enterprises/my-enterprise/\n  app_client_id: Iv1.abc\n" +
				"  app_installation_id: 42\n  app_private_key_path: /etc/rungar/app.pem\n",
			says: "does not allow a GitHub App",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, githubBody(tt.body)))
			if err == nil {
				t.Fatal("Load() = nil, want an error")
			}
			if !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("Load() = %v, want an ErrInvalidArgument", err)
			}
			if !strings.Contains(err.Error(), tt.says) {
				t.Errorf("Load() said %q, want it to mention %q", err, tt.says)
			}
		})
	}
}

// TestInlineCredentialsAreRead checks that a secret given in the file is used
// as it stands, without going near the filesystem.
func TestInlineCredentialsAreRead(t *testing.T) {
	github := GitHubConfig{Token: "  ghp_secret\n"}

	got, err := github.ReadToken()
	if err != nil {
		t.Fatalf("ReadToken() = %v", err)
	}
	// Trimmed, as a token read from a file is: a stray newline would travel
	// into an HTTP header and be rejected for reasons that say nothing.
	if got != "ghp_secret" {
		t.Errorf("ReadToken() = %q, want it trimmed", got)
	}

	key := GitHubConfig{AppPrivateKey: "-----BEGIN KEY-----\nabc\n"}

	// A PEM key is not trimmed: its trailing newline is part of it.
	if got, err := key.ReadPrivateKey(); err != nil || got != key.AppPrivateKey {
		t.Errorf("ReadPrivateKey() = %q, %v; want the key as given", got, err)
	}
}

// TestCredentialSummaryHidesInlineSecrets is the one that matters: this report
// is the sort of thing that ends up pasted into an issue.
func TestCredentialSummaryHidesInlineSecrets(t *testing.T) {
	tests := []struct {
		name   string
		github GitHubConfig
		says   string
	}{
		{
			name:   "an inline key",
			github: GitHubConfig{AppClientID: "Iv1.abc", AppInstallationID: 42, AppPrivateKey: "SECRET-KEY"},
			says:   "given inline",
		},
		{
			name:   "an inline token",
			github: GitHubConfig{Token: "ghp_SECRET"},
			says:   "given inline",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.github.Summary()

			if !strings.Contains(got, tt.says) {
				t.Errorf("%q does not say %q", got, tt.says)
			}
			for _, secret := range []string{"SECRET-KEY", "ghp_SECRET"} {
				if strings.Contains(got, secret) {
					t.Errorf("the summary leaked a secret: %q", got)
				}
			}
		})
	}
}
