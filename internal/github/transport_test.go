// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package github

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

// authServer answers every request with the Authorization it was made with.
func authServer(t *testing.T, mint http.HandlerFunc) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && mint != nil {
			mint(w, r)
			return
		}
		_, _ = w.Write([]byte(r.Header.Get("Authorization")))
	}))
	t.Cleanup(server.Close)

	return server
}

// roundTripAuthorization is what a request through rt was authenticated with.
func roundTripAuthorization(t *testing.T, rt http.RoundTripper, server *httptest.Server) string {
	t.Helper()

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/anything", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)

	if req.Header.Get("Authorization") != "" {
		t.Error("the transport changed the request it was given")
	}

	return string(b)
}

func TestTokenTransportSendsBearerToken(t *testing.T) {
	server := authServer(t, nil)

	if got := roundTripAuthorization(t, &TokenTransport{Token: "ghp_x"}, server); got != "Bearer ghp_x" {
		t.Errorf("Authorization = %q, want the token", got)
	}
}

func TestAppTransportMintsReusesAndRenewsToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	var mints atomic.Int32
	server := authServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/installations/42/access_tokens" {
			http.NotFound(w, r)
			return
		}

		// Signed by the App's key and issued by its client ID. Its dates are
		// the transport's clock's, which the test winds on.
		claims := jwt.RegisteredClaims{}
		signed := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		parser := jwt.NewParser(jwt.WithoutClaimsValidation())
		if _, err := parser.ParseWithClaims(signed, &claims, func(*jwt.Token) (any, error) { return &key.PublicKey, nil }); err != nil ||
			claims.Issuer != "Iv1.abc" {
			http.Error(w, "bad JWT", http.StatusUnauthorized)
			return
		}

		mints.Add(1)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "ghs_installation", "expires_at": time.Now().Add(time.Hour)})
	})

	tr, err := NewAppTransport("https://github.com/o/r", "Iv1.abc", 42, keyPEM, nil)
	if err != nil {
		t.Fatal(err)
	}
	tr.api, _ = url.Parse(server.URL)

	now := time.Now()
	tr.now = func() time.Time { return now }

	if got := roundTripAuthorization(t, tr, server); got != "Bearer ghs_installation" {
		t.Fatalf("Authorization = %q, want the installation token", got)
	}

	// Kept while it is good, and a new one minted when it is about to
	// expire.
	_ = roundTripAuthorization(t, tr, server)
	if got := mints.Load(); got != 1 {
		t.Errorf("minted %d installation tokens, want the first one kept", got)
	}

	now = now.Add(time.Hour)
	_ = roundTripAuthorization(t, tr, server)
	if got := mints.Load(); got != 2 {
		t.Errorf("minted %d installation tokens, want a new one once the first expires", got)
	}
}

func TestAppTransportRefusesABadKey(t *testing.T) {
	if _, err := NewAppTransport("https://github.com/o", "Iv1.abc", 42, []byte("not a key"), nil); err == nil {
		t.Error("NewAppTransport() = nil, want an error for a key that does not parse")
	}
}
