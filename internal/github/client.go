// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package github is a minimal client of GitHub's REST API, for what the scale
// set API does not say: whether each runner is online and busy. It needs only
// read access to self-hosted runners.
//
// Requests are authenticated by the HTTP client's transport: a TokenTransport,
// or an AppTransport for a GitHub App.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Runner is one self-hosted runner, as the API reports it.
type Runner struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Busy   bool   `json:"busy"`
}

// Online reports whether the runner is connected to GitHub.
func (r Runner) Online() bool { return r.Status == "online" }

// Client is GitHub's REST API for one enterprise, organisation or repository.
// It is safe for concurrent use.
type Client struct {
	http      *http.Client
	api       *url.URL
	scopePath string
}

// NewClient returns a client for the enterprise, organisation or repository
// configURL names. hc's transport authenticates the requests.
func NewClient(configURL string, hc *http.Client) (*Client, error) {
	api, scope, err := restEndpoint(configURL)
	if err != nil {
		return nil, err
	}

	return &Client{http: hc, api: api, scopePath: scope}, nil
}

// ListRunners returns every self-hosted runner of the scope, following
// pagination.
func (c *Client) ListRunners(ctx context.Context) ([]Runner, error) {
	var runners []Runner

	next := c.api.JoinPath(c.scopePath, "actions/runners").String() + "?per_page=100"
	for next != "" {
		var page struct {
			Runners []Runner `json:"runners"`
		}

		link, err := c.get(ctx, next, &page)
		if err != nil {
			return nil, err
		}

		runners = append(runners, page.Runners...)
		next = nextPage(link)
	}

	return runners, nil
}

// get decodes the JSON response to a GET of u into into, and returns the
// response's Link header.
func (c *Client) get(ctx context.Context, u string, into any) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	setAPIHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("github: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only

	if resp.StatusCode != http.StatusOK {
		return "", newAPIError(resp)
	}

	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		return "", fmt.Errorf("github: GET %s: %w", req.URL.Path, err)
	}

	return resp.Header.Get("Link"), nil
}

// nextPage returns the next page's URL from a Link header, or "" on the last
// page.
func nextPage(link string) string {
	for part := range strings.SplitSeq(link, ",") {
		target, params, ok := strings.Cut(strings.TrimSpace(part), ";")
		if ok && strings.Contains(params, `rel="next"`) {
			return strings.Trim(strings.TrimSpace(target), "<>")
		}
	}

	return ""
}

// setAPIHeaders sets the headers every REST API request carries.
func setAPIHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}
