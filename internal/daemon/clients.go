// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/actions/scaleset"
	"github.com/hashicorp/go-retryablehttp"

	"github.com/konradasb/rungar/internal/config"
	"github.com/konradasb/rungar/internal/github"
)

// requestCounter counts requests to GitHub by status code, or "error".
// *metrics.Metrics implements it.
type requestCounter interface {
	CountGitHubRequest(code string)
}

// newScaleSetClient returns a client of GitHub's scale set API. The client
// insists on its own transport, so attempts are counted from the retry
// policy, which sees each one.
func newScaleSetClient(gh config.GitHubConfig, counter requestCounter) (*scaleset.Client, error) {
	retrying := retryablehttp.NewClient()
	retrying.RetryMax = 4
	retrying.RetryWaitMax = 30 * time.Second
	retrying.CheckRetry = func(ctx context.Context, resp *http.Response, err error) (bool, error) {
		countRequest(ctx, counter, resp, err)
		return retryablehttp.DefaultRetryPolicy(ctx, resp, err)
	}
	httpOption := scaleset.WithRetryableHTTPClint(retrying)

	if gh.UsesApp() {
		key, err := gh.ReadPrivateKey()
		if err != nil {
			return nil, err
		}

		client, err := scaleset.NewClientWithGitHubApp(scaleset.ClientWithGitHubAppConfig{
			GitHubConfigURL: gh.URL,
			GitHubAppAuth: scaleset.GitHubAppAuth{
				ClientID: gh.AppClientID, InstallationID: gh.AppInstallationID, PrivateKey: key,
			},
		}, httpOption)
		if err != nil {
			return nil, fmt.Errorf("github app credentials: %w", err)
		}

		return client, nil
	}

	token, err := gh.ReadToken()
	if err != nil {
		return nil, err
	}

	client, err := scaleset.NewClientWithPersonalAccessToken(
		scaleset.NewClientWithPersonalAccessTokenConfig{GitHubConfigURL: gh.URL, PersonalAccessToken: token},
		httpOption)
	if err != nil {
		return nil, fmt.Errorf("github token: %w", err)
	}

	return client, nil
}

// newRESTClient returns a client of GitHub's REST API. Every request is
// counted, including an App's installation token requests.
func newRESTClient(gh config.GitHubConfig, counter requestCounter) (*github.Client, error) {
	counted := countingTransport{next: http.DefaultTransport, counter: counter}

	var auth http.RoundTripper

	if gh.UsesApp() {
		key, err := gh.ReadPrivateKey()
		if err != nil {
			return nil, err
		}

		auth, err = github.NewAppTransport(gh.URL, gh.AppClientID, gh.AppInstallationID, []byte(key), counted)
		if err != nil {
			return nil, err
		}
	} else {
		token, err := gh.ReadToken()
		if err != nil {
			return nil, err
		}

		auth = &github.TokenTransport{Token: token, Base: counted}
	}

	return github.NewClient(gh.URL, &http.Client{Transport: auth, Timeout: 30 * time.Second})
}

// countRequest counts one attempt at a request. An attempt whose context
// ended is not counted.
func countRequest(ctx context.Context, counter requestCounter, resp *http.Response, err error) {
	switch {
	case ctx.Err() != nil:
	case err != nil || resp == nil:
		counter.CountGitHubRequest("error")
	default:
		counter.CountGitHubRequest(strconv.Itoa(resp.StatusCode))
	}
}

// countingTransport is an http.RoundTripper that counts every request.
type countingTransport struct {
	next    http.RoundTripper
	counter requestCounter
}

func (t countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	countRequest(req.Context(), t.counter, resp, err)

	return resp, err
}
