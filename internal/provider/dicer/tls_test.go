// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// authority is a certificate authority for a test, which issues the daemon's
// certificate and Rungar's.
type authority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newAuthority(t *testing.T, name string) *authority {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}

	return &authority{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// issue returns a certificate and key, in PEM, for a server at 127.0.0.1 or
// for a client.
func (a *authority) issue(t *testing.T, name string, serial int64, usage x509.ExtKeyUsage) (certPEM, keyPEM []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, a.cert, &key.PublicKey, a.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

func write(t *testing.T, dir, name string, b []byte) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

// serveTLS runs a daemon on 127.0.0.1 that presents a certificate from ca and
// accepts only clients with one from clients, as dicerd does with
// api.tcp.tls.client_ca_file set. It returns the daemon's address.
func serveTLS(t *testing.T, ca, clients *authority) string {
	t.Helper()

	certPEM, keyPEM := ca.issue(t, "dicerd", 2, x509.ExtKeyUsageServerAuth)
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(clients.cert)

	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
	})))
	dicerdv1.RegisterDaemonServiceServer(server, &daemon{})

	var lc net.ListenConfig

	listener, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	return listener.Addr().String()
}

// reach connects to a daemon as the provider would, and reports whether it
// answered.
func reach(t *testing.T, config Config) bool {
	t.Helper()

	c, err := config.Open(nil)
	if err != nil {
		t.Fatalf("Connect() = %v", err)
	}
	defer c.Close()

	_, err = c.List(context.Background(), nil)

	return err == nil
}

// TestMutualTLS checks the whole of what TLS is for: Rungar verifies the
// daemon against its authority, and the daemon lets in only a client with a
// certificate it trusts.
func TestMutualTLS(t *testing.T) {
	dir := t.TempDir()
	ca, clients := newAuthority(t, "servers"), newAuthority(t, "clients")
	address := serveTLS(t, ca, clients)

	certPEM, keyPEM := clients.issue(t, "rungar", 3, x509.ExtKeyUsageClientAuth)
	good := &TLS{
		CAFile:   write(t, dir, "ca.pem", ca.pem),
		CertFile: write(t, dir, "client.pem", certPEM),
		KeyFile:  write(t, dir, "client-key.pem", keyPEM),
	}

	if !reach(t, Config{Address: address, TLS: good, Timeout: 5 * time.Second}) {
		t.Fatal("the daemon could not be reached with the right certificates")
	}

	t.Run("an untrusted daemon", func(t *testing.T) {
		other := newAuthority(t, "someone else")
		wrongCA := *good
		wrongCA.CAFile = write(t, dir, "other-ca.pem", other.pem)

		if reach(t, Config{Address: address, TLS: &wrongCA, Timeout: 2 * time.Second}) {
			t.Error("a daemon whose certificate the authority did not issue was trusted")
		}
	})

	t.Run("no client certificate", func(t *testing.T) {
		if reach(t, Config{Address: address, TLS: &TLS{CAFile: good.CAFile}, Timeout: 2 * time.Second}) {
			t.Error("the daemon let in a client with no certificate")
		}
	})

	t.Run("a client certificate the daemon does not trust", func(t *testing.T) {
		stranger := newAuthority(t, "strangers")
		certPEM, keyPEM := stranger.issue(t, "intruder", 4, x509.ExtKeyUsageClientAuth)
		wrong := &TLS{
			CAFile:   good.CAFile,
			CertFile: write(t, dir, "intruder.pem", certPEM),
			KeyFile:  write(t, dir, "intruder-key.pem", keyPEM),
		}

		if reach(t, Config{Address: address, TLS: wrong, Timeout: 2 * time.Second}) {
			t.Error("the daemon let in a client whose certificate it does not trust")
		}
	})

	t.Run("a renewed certificate is picked up", func(t *testing.T) {
		// Replaced on disk after the configuration was read: the next
		// connection must present the new one, without a restart.
		cfg, err := good.Load()
		if err != nil {
			t.Fatal(err)
		}

		renewedPEM, renewedKey := clients.issue(t, "rungar", 5, x509.ExtKeyUsageClientAuth)
		write(t, dir, "client.pem", renewedPEM)
		write(t, dir, "client-key.pem", renewedKey)

		got, err := cfg.GetClientCertificate(nil)
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := x509.ParseCertificate(got.Certificate[0])
		if err != nil {
			t.Fatal(err)
		}
		if leaf.SerialNumber.Int64() != 5 {
			t.Errorf("presented certificate %d, want the renewed one", leaf.SerialNumber.Int64())
		}
	})
}

func TestTLSConfigRefuses(t *testing.T) {
	dir := t.TempDir()

	for name, tt := range map[string]*TLS{
		"a CA file that is not there":    {CAFile: filepath.Join(dir, "missing.pem")},
		"a CA file with no certificates": {CAFile: write(t, dir, "empty.pem", []byte("not a certificate\n"))},
		"a key pair that is not one":     {CertFile: write(t, dir, "c.pem", []byte("x")), KeyFile: write(t, dir, "k.pem", []byte("y"))},
		"a certificate without its key":  {CertFile: "/c.pem"},
		"nothing configured at all":      {},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := tt.Load(); err == nil {
				t.Error("Config() = nil, want an error")
			}
		})
	}
}
