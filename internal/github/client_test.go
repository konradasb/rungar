// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

// runnersAPI serves a repository's runners, perPage to a page, linking each
// page to the next as GitHub does.
func runnersAPI(t *testing.T, runners []Runner, perPage int) *httptest.Server {
	t.Helper()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/actions/runners" {
			http.NotFound(w, r)
			return
		}

		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		page = max(page, 1)
		from := min((page-1)*perPage, len(runners))
		to := min(from+perPage, len(runners))

		if to < len(runners) {
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?page=%d>; rel="next", <%s%s?page=9>; rel="last"`,
				server.URL, r.URL.Path, page+1, server.URL, r.URL.Path))
		}

		_ = json.NewEncoder(w).Encode(map[string]any{"total_count": len(runners), "runners": runners[from:to]})
	}))
	t.Cleanup(server.Close)

	return server
}

// clientOf returns a client of a fake API, for repository o/r.
func clientOf(t *testing.T, server *httptest.Server) *Client {
	t.Helper()

	c, err := NewClient("https://github.com/o/r", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	c.api, _ = url.Parse(server.URL)

	return c
}

func TestListRunnersFollowsEveryPage(t *testing.T) {
	var runners []Runner
	for i := range 250 {
		runners = append(runners, Runner{ID: int64(i), Name: fmt.Sprintf("rungar-%d", i), Status: "online"})
	}

	got, err := clientOf(t, runnersAPI(t, runners, 100)).ListRunners(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 250 || got[249].Name != "rungar-249" || !got[249].Online() {
		t.Errorf("listed %d runners, last %+v; want all 250", len(got), got[len(got)-1])
	}
}

func TestListRunnersReportsGitHubsRefusal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	t.Cleanup(server.Close)

	_, err := clientOf(t, server).ListRunners(context.Background())

	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized || apiErr.Message != "Bad credentials" {
		t.Errorf("ListRunners() = %v, want GitHub's refusal as an *APIError", err)
	}
}

func TestNextPage(t *testing.T) {
	for link, want := range map[string]string{
		"": "",
		`<https://api.github.com/x?page=2>; rel="next", <https://api.github.com/x?page=5>; rel="last"`:  "https://api.github.com/x?page=2",
		`<https://api.github.com/x?page=1>; rel="prev", <https://api.github.com/x?page=1>; rel="first"`: "",
	} {
		if got := nextPage(link); got != want {
			t.Errorf("nextPage(%q) = %q, want %q", link, got, want)
		}
	}
}
