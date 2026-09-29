// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package github

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestAPIErrorCarriesGitHubsMessage checks an unexpected response is read
// into an APIError with GitHub's JSON message, or the start of the body when
// it is not JSON.
func TestAPIErrorCarriesGitHubsMessage(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "json", body: `{"message": "Bad credentials"}`, want: "Bad credentials"},
		{name: "plain text", body: " upstream timed out \n", want: "upstream timed out"},
		{name: "json without a message", body: `{"error": "x"}`, want: `{"error": "x"}`},
		{name: "a long body is cut", body: strings.Repeat("a", 1000), want: strings.Repeat("a", 512)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://api.github.com/repos/o/r/actions/runners?page=2", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp := &http.Response{
				StatusCode: http.StatusUnauthorized,
				Body:       io.NopCloser(strings.NewReader(tt.body)),
				Request:    req,
			}

			got := newAPIError(resp)

			if got.Method != http.MethodGet || got.Path != "/repos/o/r/actions/runners" ||
				got.StatusCode != http.StatusUnauthorized || got.Message != tt.want {
				t.Errorf("newAPIError() = %+v, want GET /repos/o/r/actions/runners 401 %q", got, tt.want)
			}
		})
	}
}

// TestAPIErrorNamesTheRequestAndStatus checks the error says which request
// GitHub refused, and how.
func TestAPIErrorNamesTheRequestAndStatus(t *testing.T) {
	err := &APIError{Method: "POST", Path: "/app/installations/1/access_tokens", StatusCode: 404, Message: "Not Found"}

	want := "github: POST /app/installations/1/access_tokens: 404 Not Found: Not Found"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
