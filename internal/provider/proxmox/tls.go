// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package proxmox

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/konradasb/rungar/internal/errdefs"
)

// TLS is how the API's certificate is verified.
type TLS struct {
	// CAFile holds the PEM authorities the API's certificate is checked
	// against: the cluster's own, /etc/pve/pve-root-ca.pem, for a
	// certificate Proxmox VE made itself.
	CAFile string `yaml:"ca_file,omitempty"`

	// InsecureSkipVerify accepts any certificate, which lets whoever is
	// between Rungar and the API read the token. For a test cluster only.
	InsecureSkipVerify bool `yaml:"insecure_skip_verify,omitempty"`
}

// Validate checks the settings are a usable combination, without reading the
// files.
func (t *TLS) Validate() error {
	if t == nil {
		return nil
	}

	switch {
	case t.CAFile != "" && t.InsecureSkipVerify:
		return errdefs.InvalidArgument("tls: give ca_file or insecure_skip_verify, not both")
	case t.CAFile == "" && !t.InsecureSkipVerify:
		return errdefs.InvalidArgument(
			"tls: nothing is configured; give ca_file, or leave tls out to use this machine's root CAs")
	}

	return nil
}

// Load returns the TLS configuration. A nil TLS verifies against this
// machine's root CAs.
func (t *TLS) Load() (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if t == nil {
		return cfg, nil
	}

	if err := t.Validate(); err != nil {
		return nil, err
	}

	if t.InsecureSkipVerify {
		cfg.InsecureSkipVerify = true
		return cfg, nil
	}

	pem, err := os.ReadFile(t.CAFile)
	if err != nil {
		return nil, fmt.Errorf("read the certificate authorities: %w", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%s holds no certificates; want PEM", t.CAFile)
	}
	cfg.RootCAs = pool

	return cfg, nil
}
