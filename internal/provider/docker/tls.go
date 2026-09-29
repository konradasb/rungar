// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package docker

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/konradasb/rungar/internal/errdefs"
)

// TLS is how a Docker daemon's TCP address is reached: CAFile verifies the
// daemon, and CertFile and KeyFile identify Rungar to it. Either half may be
// given alone.
type TLS struct {
	// CertFile and KeyFile are Rungar's certificate and private key, in PEM.
	// Give both or neither.
	CertFile string `yaml:"cert_file,omitempty"`
	KeyFile  string `yaml:"key_file,omitempty"`

	// CAFile holds the PEM authorities a host's certificate is checked
	// against. Empty uses this machine's root CAs.
	CAFile string `yaml:"ca_file,omitempty"`

	// ServerName is the name a host's certificate must carry. Empty uses
	// the host from its address.
	ServerName string `yaml:"server_name,omitempty"`
}

// Validate checks the settings are a usable combination, without reading the
// files.
func (t *TLS) Validate() error {
	if t == nil {
		return nil
	}

	switch {
	case t.CertFile != "" && t.KeyFile == "":
		return errdefs.InvalidArgument("tls: cert_file is set without key_file")
	case t.KeyFile != "" && t.CertFile == "":
		return errdefs.InvalidArgument("tls: key_file is set without cert_file")
	case t.CertFile == "" && t.CAFile == "":
		return errdefs.InvalidArgument(
			"tls: nothing is configured; give ca_file, or cert_file and key_file, or leave tls out entirely")
	}

	return nil
}

// Load reads the files into a TLS configuration. The client certificate is
// read again on every handshake, so that a renewed one is used without a
// restart.
func (t *TLS) Load() (*tls.Config, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}

	cfg := &tls.Config{MinVersion: tls.VersionTLS13, ServerName: t.ServerName}

	if t.CertFile != "" {
		// Read once now, so that a bad pair is reported with the
		// configuration.
		if _, err := tls.LoadX509KeyPair(t.CertFile, t.KeyFile); err != nil {
			return nil, fmt.Errorf("load the client certificate: %w", err)
		}

		cfg.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			cert, err := tls.LoadX509KeyPair(t.CertFile, t.KeyFile)
			if err != nil {
				return nil, fmt.Errorf("load the client certificate: %w", err)
			}

			return &cert, nil
		}
	}

	if t.CAFile != "" {
		pool, err := certPool(t.CAFile)
		if err != nil {
			return nil, err
		}
		cfg.RootCAs = pool
	}

	return cfg, nil
}

// certPool reads a PEM file of certificate authorities.
func certPool(path string) (*x509.CertPool, error) {
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
