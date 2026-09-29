// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package provider_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/provider"
)

func TestCertPoolReadsTheAuthorities(t *testing.T) {
	path := writeFile(t, "ca.pem", selfSignedPEM(t))

	pool, err := provider.CertPool(path)
	if err != nil {
		t.Fatalf("CertPool() = %v", err)
	}
	if pool == nil {
		t.Fatal("CertPool() = nil pool")
	}
}

func TestCertPoolRefuses(t *testing.T) {
	tests := []struct {
		name, path, says string
	}{
		{"missing file", filepath.Join(t.TempDir(), "missing.pem"), "read the certificate authorities"},
		{"no certificates", writeFile(t, "empty.pem", []byte("not PEM\n")), "holds no certificates"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := provider.CertPool(tt.path); err == nil || !strings.Contains(err.Error(), tt.says) {
				t.Errorf("CertPool() = %v, want an error saying %q", err, tt.says)
			}
		})
	}
}

// writeFile writes data to a file of this name in a temporary directory, and
// returns its path.
func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

// selfSignedPEM returns a self-signed certificate authority, PEM-encoded.
func selfSignedPEM(t *testing.T) []byte {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
