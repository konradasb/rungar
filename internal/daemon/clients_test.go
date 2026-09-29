// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/konradasb/rungar/internal/config"
)

// discardCounter counts nothing.
type discardCounter struct{}

func (discardCounter) CountGitHubRequest(string) {}

func TestClientsFromAnApp(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "app.pem")

	if err := os.WriteFile(key, []byte(testPrivateKey), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.GitHub = config.GitHubConfig{
		URL:         "https://github.com/my-org",
		AppClientID: "Iv1.abc", AppInstallationID: 1, AppPrivateKeyPath: key,
	}

	if _, err := newScaleSetClient(cfg.GitHub, discardCounter{}); err != nil {
		t.Errorf("newScaleSetClient() = %v", err)
	}
	if _, err := newRESTClient(cfg.GitHub, discardCounter{}); err != nil {
		t.Errorf("newRESTClient() = %v", err)
	}
}

func TestClientsFromAToken(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token")

	if err := os.WriteFile(tokenPath, []byte("ghp_secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.GitHub = config.GitHubConfig{URL: "https://github.com/my-org", TokenPath: tokenPath}

	if _, err := newScaleSetClient(cfg.GitHub, discardCounter{}); err != nil {
		t.Errorf("newScaleSetClient() = %v", err)
	}
	if _, err := newRESTClient(cfg.GitHub, discardCounter{}); err != nil {
		t.Errorf("newRESTClient() = %v", err)
	}
}

func TestClientsReportsAMissingCredential(t *testing.T) {
	tests := []struct {
		name   string
		github config.GitHubConfig
	}{
		{
			name: "the app key is not there",
			github: config.GitHubConfig{
				URL: "https://github.com/my-org", AppClientID: "Iv1.abc", AppInstallationID: 1,
				AppPrivateKeyPath: "/nonexistent/app.pem",
			},
		},
		{
			name: "the token file is not there",
			github: config.GitHubConfig{
				URL:       "https://github.com/my-org",
				TokenPath: "/nonexistent/token",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.GitHub = tt.github

			if _, err := newScaleSetClient(cfg.GitHub, discardCounter{}); err == nil {
				t.Error("newScaleSetClient() = nil, want an error")
			}
			if _, err := newRESTClient(cfg.GitHub, discardCounter{}); err == nil {
				t.Error("newRESTClient() = nil, want an error")
			}
		})
	}
}

// testPrivateKey is a throwaway RSA key, generated for this test alone. It
// signs nothing that exists.
const testPrivateKey = `-----BEGIN RSA PRIVATE KEY-----
MIIBOgIBAAJBAKj34GkxFhD90vcNLYLInFEX6Ppy1tPf9Cnzj4p4WGeKLs1Pt8Qu
KUpRKfFLfRYC9AIKjbJTWit+CqvjWYzvQwECAwEAAQJAIJLixBy2qpFoS4DSmoEm
o3qGy0t6z09AIJtH+5OeRV1be+N4cDYJKffGzDa88vQENZiRm0GRq6a+HPGQMd2k
TQIhAKMSvzIBnni7ot/OSie2TmJLY4SwTQAevXysE2RbFDYdAiEBCUEaRQnMnbp7
9mxDXDf6AU0cN/RPBjb9qSHDcWZHGzUCIG2Es59z8ugGrDY+pxLQnwfotadxd+Uy
v/Ow5T0q5gIJAiEAyS4RaI9YG8EWx/2w0T67ZUVAw8eOMB6BIUg0Xcu+3okCIBOs
/5OiPgoTdSy7bcF9IGpSE8ZgGKzgYQVZeN97YE00
-----END RSA PRIVATE KEY-----`

// requestLog is a requestCounter that remembers what it was told.
type requestLog struct {
	mu    sync.Mutex
	codes []string
}

func (l *requestLog) CountGitHubRequest(code string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.codes = append(l.codes, code)
}

func (l *requestLog) got() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return slices.Clone(l.codes)
}

func TestCountRequest(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		ctx  context.Context
		resp *http.Response
		err  error
		want []string
	}{
		{name: "an answer", ctx: context.Background(), resp: &http.Response{StatusCode: http.StatusOK}, want: []string{"200"}},
		{name: "a refusal", ctx: context.Background(), resp: &http.Response{StatusCode: http.StatusUnauthorized}, want: []string{"401"}},
		{name: "no answer", ctx: context.Background(), err: errors.New("connection refused"), want: []string{"error"}},
		{name: "abandoned", ctx: cancelled, err: context.Canceled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var log requestLog
			countRequest(tt.ctx, &log, tt.resp, tt.err)

			if got := log.got(); !slices.Equal(got, tt.want) {
				t.Errorf("counted %v, want %v", got, tt.want)
			}
		})
	}
}

// TestScaleSetRequestsAreCounted checks the scale set client's requests are
// counted, through the library, by a GitHub that refuses the credential.
func TestScaleSetRequestsAreCounted(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
	}))
	defer gh.Close()

	var log requestLog

	client, err := newScaleSetClient(config.GitHubConfig{URL: gh.URL + "/my-org", Token: "ghp_expired"}, &log)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := client.GetRunnerGroupByName(context.Background(), "Default"); err == nil {
		t.Fatal("GetRunnerGroupByName() = nil, want GitHub's refusal")
	}

	if got := log.got(); !slices.Equal(got, []string{"401"}) {
		t.Errorf("counted %v, want one 401", got)
	}
}

// TestRESTRequestsAreCounted checks the REST client's requests are counted,
// by a GitHub that answers.
func TestRESTRequestsAreCounted(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"total_count":0,"runners":[]}`))
	}))
	defer gh.Close()

	var log requestLog

	client, err := newRESTClient(config.GitHubConfig{URL: gh.URL + "/my-org", Token: "ghp_x"}, &log)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := client.ListRunners(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := log.got(); !slices.Equal(got, []string{"200"}) {
		t.Errorf("counted %v, want one 200", got)
	}
}
