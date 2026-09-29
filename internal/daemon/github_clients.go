// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	ghscaleset "github.com/actions/scaleset"
	"github.com/hashicorp/go-retryablehttp"

	"github.com/konradasb/rungar/internal/config"
	"github.com/konradasb/rungar/internal/github"
	"github.com/konradasb/rungar/internal/metrics"
)

// gitHubRequestRecorder counts requests to GitHub by status code, or
// "error". *metrics.Metrics implements it.
type gitHubRequestRecorder interface {
	CountGitHubRequest(code string)
}

var _ gitHubRequestRecorder = (*metrics.Metrics)(nil)

// newScaleSetClient returns a client of GitHub's scale set API. The client
// insists on its own transport, so attempts are counted from the retrying
// client's hooks. The library replaces the retry policy once it has an
// Actions service token, so attempts with no answer go uncounted after that.
func newScaleSetClient(gh config.GitHub, recorder gitHubRequestRecorder) (*ghscaleset.Client, error) {
	retrying := retryablehttp.NewClient()
	retrying.RetryMax = 4
	retrying.RetryWaitMax = 30 * time.Second
	retrying.ResponseLogHook = func(_ retryablehttp.Logger, resp *http.Response) {
		recorder.CountGitHubRequest(strconv.Itoa(resp.StatusCode))
	}
	retrying.CheckRetry = func(ctx context.Context, resp *http.Response, err error) (bool, error) {
		if err != nil {
			countRequest(ctx, recorder, resp, err)
		}

		return retryablehttp.DefaultRetryPolicy(ctx, resp, err)
	}
	httpOption := ghscaleset.WithRetryableHTTPClint(retrying)

	secret, err := readGitHubCredential(gh)
	if err != nil {
		return nil, err
	}

	if gh.UsesApp() {
		client, err := ghscaleset.NewClientWithGitHubApp(ghscaleset.ClientWithGitHubAppConfig{
			GitHubConfigURL: gh.URL,
			GitHubAppAuth: ghscaleset.GitHubAppAuth{
				ClientID:       gh.AppClientID,
				InstallationID: gh.AppInstallationID,
				PrivateKey:     secret,
			},
		}, httpOption)
		if err != nil {
			return nil, fmt.Errorf("github app credentials: %w", err)
		}

		return client, nil
	}

	client, err := ghscaleset.NewClientWithPersonalAccessToken(
		ghscaleset.NewClientWithPersonalAccessTokenConfig{GitHubConfigURL: gh.URL, PersonalAccessToken: secret},
		httpOption)
	if err != nil {
		return nil, fmt.Errorf("github token: %w", err)
	}

	return client, nil
}

// newRESTClient returns a client of GitHub's REST API. Every request is
// counted, including an App's installation token requests.
func newRESTClient(gh config.GitHub, recorder gitHubRequestRecorder) (*github.Client, error) {
	counted := countingTransport{next: http.DefaultTransport, recorder: recorder}

	secret, err := readGitHubCredential(gh)
	if err != nil {
		return nil, err
	}

	var auth http.RoundTripper
	if gh.UsesApp() {
		auth, err = github.NewAppTransport(gh.URL, gh.AppClientID, gh.AppInstallationID, []byte(secret), counted)
		if err != nil {
			return nil, fmt.Errorf("github app credentials: %w", err)
		}
	} else {
		auth = &github.TokenTransport{Token: secret, Base: counted}
	}

	client, err := github.NewClient(gh.URL, &http.Client{Transport: auth, Timeout: 30 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("github client: %w", err)
	}

	return client, nil
}

// readGitHubCredential returns the App's private key when a GitHub App is
// configured, and the personal access token otherwise.
func readGitHubCredential(gh config.GitHub) (string, error) {
	if gh.UsesApp() {
		return gh.ReadPrivateKey()
	}

	return gh.ReadToken()
}

// countRequest counts one attempt at a request. An attempt whose context
// ended is not counted.
func countRequest(ctx context.Context, recorder gitHubRequestRecorder, resp *http.Response, err error) {
	switch {
	case ctx.Err() != nil:
	case err != nil || resp == nil:
		recorder.CountGitHubRequest("error")
	default:
		recorder.CountGitHubRequest(strconv.Itoa(resp.StatusCode))
	}
}

// countingTransport is an http.RoundTripper that counts every request.
type countingTransport struct {
	next     http.RoundTripper
	recorder gitHubRequestRecorder
}

func (t countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	countRequest(req.Context(), t.recorder, resp, err)

	return resp, err
}
