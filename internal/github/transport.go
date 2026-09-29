// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package github

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"golang.org/x/sync/singleflight"
)

// TokenTransport authenticates every request with a personal access token.
type TokenTransport struct {
	Token string

	// Base makes the requests; nil is http.DefaultTransport.
	Base http.RoundTripper
}

// RoundTrip implements http.RoundTripper.
func (t *TokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return orDefault(t.Base).RoundTrip(withBearer(req, t.Token))
}

// AppTransport authenticates every request as a GitHub App installation. It
// mints an installation token when it has none or its token is about to
// expire.
type AppTransport struct {
	clientID       string
	installationID int64
	key            *rsa.PrivateKey
	api            *url.URL
	base           http.RoundTripper

	// now is time.Now, replaced in tests.
	now func() time.Time

	// mints makes concurrent callers share one token mint.
	mints singleflight.Group

	mu      sync.Mutex
	token   string
	expires time.Time
}

// NewAppTransport returns a transport that authenticates as the App's
// installation on the GitHub configURL is on. base makes every request,
// token minting included; nil means http.DefaultTransport.
func NewAppTransport(configURL, clientID string, installationID int64, keyPEM []byte, base http.RoundTripper,
) (*AppTransport, error) {
	api, _, err := restEndpoint(configURL)
	if err != nil {
		return nil, err
	}

	key, err := jwt.ParseRSAPrivateKeyFromPEM(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("github: the App's private key: %w", err)
	}

	return &AppTransport{
		clientID:       clientID,
		installationID: installationID,
		key:            key,
		api:            api,
		base:           base,
		now:            time.Now,
	}, nil
}

// RoundTrip implements http.RoundTripper.
func (t *AppTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := t.installationToken(req.Context())
	if err != nil {
		return nil, err
	}

	return orDefault(t.base).RoundTrip(withBearer(req, token))
}

// mintTimeout bounds minting an installation token, which runs apart from any
// one caller's context because every caller waiting for it shares it.
const mintTimeout = time.Minute

// installationToken returns the installation token, minting one if there is
// none or it expires within five minutes. Concurrent callers share one mint,
// and each stops waiting when its own ctx is done.
func (t *AppTransport) installationToken(ctx context.Context) (string, error) {
	if token, ok := t.cachedToken(); ok {
		return token, nil
	}

	result := t.mints.DoChan("", func() (any, error) {
		// A caller may have stored a fresh token since this one looked.
		if token, ok := t.cachedToken(); ok {
			return token, nil
		}

		mintCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), mintTimeout)
		defer cancel()

		token, expires, err := t.mintToken(mintCtx)
		if err != nil {
			return "", err
		}

		t.mu.Lock()
		t.token, t.expires = token, expires
		t.mu.Unlock()

		return token, nil
	})

	select {
	case r := <-result:
		if r.Err != nil {
			return "", r.Err
		}
		token, _ := r.Val.(string)

		return token, nil
	case <-ctx.Done():
		return "", fmt.Errorf("github: installation token: %w", ctx.Err())
	}
}

// cachedToken returns the installation token, and whether it is good for more
// than five minutes.
func (t *AppTransport) cachedToken() (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.token == "" || !t.now().Before(t.expires.Add(-5*time.Minute)) {
		return "", false
	}

	return t.token, true
}

// mintToken asks GitHub for a new installation token, and returns it with
// when it expires.
func (t *AppTransport) mintToken(ctx context.Context) (string, time.Time, error) {
	signed, err := t.appJWT()
	if err != nil {
		return "", time.Time{}, err
	}

	u := t.api.JoinPath("app/installations", strconv.FormatInt(t.installationID, 10), "access_tokens")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("github: installation token: %w", err)
	}
	setAPIHeaders(req)

	resp, err := orDefault(t.base).RoundTrip(withBearer(req, signed))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("github: installation token: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only

	if resp.StatusCode != http.StatusCreated {
		return "", time.Time{}, newAPIError(resp)
	}

	var body struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", time.Time{}, fmt.Errorf("github: installation token: %w", err)
	}

	return body.Token, body.ExpiresAt, nil
}

// appJWT returns the App's JWT, used to mint installation tokens. It is
// backdated a minute for clock skew, and lasts under GitHub's ten-minute
// limit.
func (t *AppTransport) appJWT() (string, error) {
	issued := t.now().Add(-time.Minute)

	return jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(issued),
		ExpiresAt: jwt.NewNumericDate(issued.Add(9 * time.Minute)),
		Issuer:    t.clientID,
	}).SignedString(t.key)
}

// withBearer returns a copy of req authenticated with token: a RoundTripper
// must not modify its request.
func withBearer(req *http.Request, token string) *http.Request {
	out := req.Clone(req.Context())
	out.Header.Set("Authorization", "Bearer "+token)

	return out
}

// orDefault returns rt, or http.DefaultTransport if rt is nil.
func orDefault(rt http.RoundTripper) http.RoundTripper {
	if rt == nil {
		return http.DefaultTransport
	}

	return rt
}
