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

// TestTokenIsTrimmed checks a token is trimmed, inline or from a file: a
// trailing newline would otherwise travel into an HTTP header, where GitHub
// rejects it for reasons that say nothing useful.
func TestTokenIsTrimmed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("  ghp_secret\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		github GitHub
	}{
		{name: "in a file", github: GitHub{TokenPath: path}},
		{name: "inline", github: GitHub{Token: "  ghp_secret\n"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.github.ReadToken()
			if err != nil {
				t.Fatalf("ReadToken() = %v", err)
			}
			if got != "ghp_secret" {
				t.Errorf("ReadToken() = %q, want it trimmed", got)
			}
		})
	}
}

// TestTokenIsEmptyWithoutAPath checks ReadToken returns an empty token, and no
// error, when no token is configured.
func TestTokenIsEmptyWithoutAPath(t *testing.T) {
	got, err := (&GitHub{}).ReadToken()
	if err != nil {
		t.Fatalf("ReadToken() = %v", err)
	}
	if got != "" {
		t.Errorf("ReadToken() = %q, want empty when no token is configured", got)
	}
}

// TestPrivateKeyIsReadAsGiven checks a key is read inline or from a file, and
// not trimmed: a PEM key's trailing newline is part of it.
func TestPrivateKeyIsReadAsGiven(t *testing.T) {
	const key = "-----BEGIN KEY-----\nabc\n"

	path := filepath.Join(t.TempDir(), "app.pem")
	if err := os.WriteFile(path, []byte(key), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		github GitHub
	}{
		{name: "in a file", github: GitHub{AppPrivateKeyPath: path}},
		{name: "inline", github: GitHub{AppPrivateKey: key}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := tt.github.ReadPrivateKey(); err != nil || got != key {
				t.Errorf("ReadPrivateKey() = %q, %v; want the key as given", got, err)
			}
		})
	}
}

// TestCheckFilesRefusesAnUnusableCredential checks every credential file
// CheckFiles cannot use is an ErrInvalidArgument naming what is wrong.
func TestCheckFilesRefusesAnUnusableCredential(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing")

	tests := []struct {
		name   string
		github GitHub
		says   string
	}{
		{
			name:   "a token file that is not there",
			github: GitHub{TokenPath: missing},
			says:   missing,
		},
		{
			name:   "an empty token file",
			github: GitHub{TokenPath: empty},
			says:   empty + " is empty; write the token into it",
		},
		{
			name:   "a key file that is not there",
			github: GitHub{AppClientID: "Iv1.abc", AppPrivateKeyPath: missing},
			says:   missing,
		},
		{
			name:   "a key that is not one",
			github: GitHub{AppClientID: "Iv1.abc", AppPrivateKeyPath: empty},
			says:   "private key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.github.CheckFiles()
			if !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Fatalf("CheckFiles() = %v, want an ErrInvalidArgument", err)
			}
			if !strings.Contains(err.Error(), tt.says) {
				t.Errorf("CheckFiles() said %q, want it to mention %q", err, tt.says)
			}
		})
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

// TestEachCredentialFormIsAccepted checks every way of giving a credential
// loads.
func TestEachCredentialFormIsAccepted(t *testing.T) {
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

// TestInvalidCredentialsAreRefused checks credentials given wrongly are
// refused, saying what.
func TestInvalidCredentialsAreRefused(t *testing.T) {
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

// TestCredentialSummaryHidesInlineSecrets checks the credential summary says a
// key or token is given inline without showing it: the summary is the sort of
// thing that ends up pasted into an issue.
func TestCredentialSummaryHidesInlineSecrets(t *testing.T) {
	tests := []struct {
		name   string
		github GitHub
		says   string
	}{
		{
			name:   "an inline key",
			github: GitHub{AppClientID: "Iv1.abc", AppInstallationID: 42, AppPrivateKey: "SECRET-KEY"},
			says:   "given inline",
		},
		{
			name:   "an inline token",
			github: GitHub{Token: "ghp_SECRET"},
			says:   "given inline",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.github.CredentialSummary()

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
