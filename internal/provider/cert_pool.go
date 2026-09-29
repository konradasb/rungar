// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package provider

import (
	"crypto/x509"
	"fmt"
	"os"
)

// CertPool reads a PEM file of certificate authorities, refusing one that
// holds none.
func CertPool(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read the certificate authorities: %w", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%s holds no certificates; want PEM", path)
	}

	return pool, nil
}
