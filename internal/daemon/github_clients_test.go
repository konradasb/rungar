// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/config"
)

// discardRecorder is a gitHubRequestRecorder that records nothing.
type discardRecorder struct{}

func (discardRecorder) CountGitHubRequest(string) {}

// TestClientsAreCreatedFromEitherCredential checks both clients are created
// from an App's key or a token, each read from its file.
func TestClientsAreCreatedFromEitherCredential(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "app.pem")
	if err := os.WriteFile(key, []byte(testPrivateKey), 0o600); err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(dir, "token")
	if err := os.WriteFile(token, []byte("ghp_secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		github config.GitHub
	}{
		{
			name: "an app",
			github: config.GitHub{
				URL:         "https://github.com/my-org",
				AppClientID: "Iv1.abc", AppInstallationID: 1, AppPrivateKeyPath: key,
			},
		},
		{
			name:   "a token",
			github: config.GitHub{URL: "https://github.com/my-org", TokenPath: token},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := newScaleSetClient(tt.github, discardRecorder{}); err != nil {
				t.Errorf("newScaleSetClient() = %v", err)
			}
			if _, err := newRESTClient(tt.github, discardRecorder{}); err != nil {
				t.Errorf("newRESTClient() = %v", err)
			}
		})
	}
}

// TestClientsReportAMissingCredential checks neither client is created when
// the App key or token file it names cannot be read.
func TestClientsReportAMissingCredential(t *testing.T) {
	tests := []struct {
		name   string
		github config.GitHub
	}{
		{
			name: "the app key is not there",
			github: config.GitHub{
				URL: "https://github.com/my-org", AppClientID: "Iv1.abc", AppInstallationID: 1,
				AppPrivateKeyPath: "/nonexistent/app.pem",
			},
		},
		{
			name: "the token file is not there",
			github: config.GitHub{
				URL:       "https://github.com/my-org",
				TokenPath: "/nonexistent/token",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := newScaleSetClient(tt.github, discardRecorder{}); err == nil {
				t.Error("newScaleSetClient() = nil, want an error")
			}
			if _, err := newRESTClient(tt.github, discardRecorder{}); err == nil {
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

// fakeGitHubRequestRecorder is a gitHubRequestRecorder that remembers what it
// was told.
type fakeGitHubRequestRecorder struct {
	mu    sync.Mutex
	codes []string
}

func (r *fakeGitHubRequestRecorder) CountGitHubRequest(code string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.codes = append(r.codes, code)
}

func (r *fakeGitHubRequestRecorder) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.codes)
}

// TestEachRequestIsCountedByItsOutcome checks a request is counted by its
// status code, or as an error when there is no response, and not at all when
// abandoned.
func TestEachRequestIsCountedByItsOutcome(t *testing.T) {
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	tests := []struct {
		name string
		ctx  context.Context
		resp *http.Response
		err  error
		want []string
	}{
		{name: "an answer", ctx: t.Context(), resp: &http.Response{StatusCode: http.StatusOK}, want: []string{"200"}},
		{
			name: "a rejection", ctx: t.Context(),
			resp: &http.Response{StatusCode: http.StatusUnauthorized}, want: []string{"401"},
		},
		{name: "no answer", ctx: t.Context(), err: errors.New("connection refused"), want: []string{"error"}},
		{name: "abandoned", ctx: cancelled, err: context.Canceled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var recorder fakeGitHubRequestRecorder
			countRequest(tt.ctx, &recorder, tt.resp, tt.err)

			if got := recorder.got(); !slices.Equal(got, tt.want) {
				t.Errorf("counted %v, want %v", got, tt.want)
			}
		})
	}
}

// TestScaleSetRequestsAreCounted checks the scale set client's requests are
// counted, through the library, by a GitHub that rejects the credential.
func TestScaleSetRequestsAreCounted(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
	}))
	defer gh.Close()

	var recorder fakeGitHubRequestRecorder

	client, err := newScaleSetClient(config.GitHub{URL: gh.URL + "/my-org", Token: "ghp_expired"}, &recorder)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := client.GetRunnerGroupByName(t.Context(), "Default"); err == nil {
		t.Fatal("GetRunnerGroupByName() = nil, want GitHub's rejection")
	}

	if got := recorder.got(); !slices.Equal(got, []string{"401"}) {
		t.Errorf("counted %v, want one 401", got)
	}
}

// TestMessageSessionPollsAreCounted checks polls are counted after the
// library has replaced the retry policy.
func TestMessageSessionPollsAreCounted(t *testing.T) {
	encode := base64.RawURLEncoding.EncodeToString
	exp := time.Now().Add(time.Hour).Unix()
	// The library reads the expiry without checking the signature.
	token := encode([]byte(`{"alg":"none"}`)) + "." + encode(fmt.Appendf(nil, `{"exp":%d}`, exp)) + "."

	var gh *httptest.Server
	gh = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/registration-token"):
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"registration"}`))
		case strings.HasSuffix(r.URL.Path, "/actions/runner-registration"):
			_, _ = fmt.Fprintf(w, `{"url":%q,"token":%q}`, gh.URL+"/tenant", token)
		case strings.HasSuffix(r.URL.Path, "/sessions"):
			_, _ = fmt.Fprintf(w, `{"sessionId":"6f1c4b52-0a5e-4d3c-9b1a-2c8e7f3d4a10",`+
				`"messageQueueUrl":%q,"messageQueueAccessToken":"queue"}`, gh.URL+"/queue")
		case r.URL.Path == "/queue":
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	}))
	defer gh.Close()

	var recorder fakeGitHubRequestRecorder

	client, err := newScaleSetClient(config.GitHub{URL: gh.URL + "/my-org", Token: "ghp_x"}, &recorder)
	if err != nil {
		t.Fatal(err)
	}

	session, err := client.MessageSessionClient(t.Context(), 1, "rungar-test")
	if err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if _, err := session.GetMessage(t.Context(), 0, 1); err != nil {
			t.Fatal(err)
		}
	}

	want := []string{"201", "200", "200", "202", "202"}
	if got := recorder.got(); !slices.Equal(got, want) {
		t.Errorf("counted %v, want %v", got, want)
	}
}

// TestRESTRequestsAreCounted checks the REST client's requests are counted,
// by a GitHub that answers.
func TestRESTRequestsAreCounted(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"total_count":0,"runners":[]}`))
	}))
	defer gh.Close()

	var recorder fakeGitHubRequestRecorder

	client, err := newRESTClient(config.GitHub{URL: gh.URL + "/my-org", Token: "ghp_x"}, &recorder)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := client.Runners(t.Context()); err != nil {
		t.Fatal(err)
	}

	if got := recorder.got(); !slices.Equal(got, []string{"200"}) {
		t.Errorf("counted %v, want one 200", got)
	}
}
