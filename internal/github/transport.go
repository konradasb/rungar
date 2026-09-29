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
	token, err := t.ensureToken(req.Context())
	if err != nil {
		return nil, err
	}

	return orDefault(t.base).RoundTrip(withBearer(req, token))
}

// ensureToken returns the installation token, minting one if there is none or
// it expires within five minutes.
func (t *AppTransport) ensureToken(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.token != "" && t.now().Before(t.expires.Add(-5*time.Minute)) {
		return t.token, nil
	}

	signed, err := t.appJWT()
	if err != nil {
		return "", err
	}

	u := t.api.JoinPath("app/installations", strconv.FormatInt(t.installationID, 10), "access_tokens")

	mint, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), nil)
	if err != nil {
		return "", err
	}
	setAPIHeaders(mint)

	resp, err := orDefault(t.base).RoundTrip(withBearer(mint, signed))
	if err != nil {
		return "", fmt.Errorf("github: installation token: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only

	if resp.StatusCode != http.StatusCreated {
		return "", newAPIError(resp)
	}

	var body struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("github: installation token: %w", err)
	}

	t.token, t.expires = body.Token, body.ExpiresAt

	return t.token, nil
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
