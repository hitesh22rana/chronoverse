//nolint:testpackage // Verifies the adapter credential loader with real TLS.
package meilisearch

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func adapterTLSIdentity(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1), DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile = filepath.Join(t.TempDir(), "cert.pem"), filepath.Join(t.TempDir(), "key.pem")
	if err = os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func TestAdapterTLSRejectsUnusableCredentialFiles(t *testing.T) {
	t.Parallel()
	cert, key := adapterTLSIdentity(t)
	_, otherKey := adapterTLSIdentity(t)
	invalid := filepath.Join(t.TempDir(), "invalid.pem")
	if err := os.WriteFile(invalid, []byte("not a PEM certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(t.TempDir(), "empty.pem")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, cert, key, ca, message string }{
		{"missing client certificate", cert + ".missing", key, cert, "client key pair"},
		{"mismatched key", cert, otherKey, cert, "client key pair"},
		{"missing CA", cert, key, cert + ".missing", "read CA"},
		{"malformed CA", cert, key, invalid, "parse CA"},
		{"empty CA", cert, key, empty, "parse CA"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := newTLSConfig(tc.cert, tc.key, tc.ca)
			if cfg != nil || status.Code(err) != codes.Internal || !strings.Contains(status.Convert(err).Message(), tc.message) {
				t.Fatalf("nil config=%t error=%v, want nil config/Internal/%q", cfg == nil, err, tc.message)
			}
		})
	}
}

// One short-lived identity serves both ends of this loopback mTLS contract.
func adapterTLSPeer(t *testing.T) (server *httptest.Server, clientConfig *tls.Config) {
	t.Helper()
	cert, key := adapterTLSIdentity(t)
	cfg, err := newTLSConfig(cert, key, cert)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MinVersion != tls.VersionTLS12 || len(cfg.Certificates) != 1 || cfg.RootCAs == nil {
		t.Fatal("TLS policy must require TLS 1.2, roots, and one client identity")
	}
	server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
			http.Error(w, "unverified client", http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = cfg.Clone()
	server.TLS.ClientCAs = cfg.RootCAs
	server.TLS.ClientAuth = tls.RequireAndVerifyClientCert
	server.StartTLS()
	t.Cleanup(server.Close)
	cfg.ServerName = "localhost"
	return server, cfg
}

func adapterTLSRequest(t *testing.T, address string, cfg *tls.Config) (*http.Response, error) {
	t.Helper()
	transport := &http.Transport{TLSClientConfig: cfg}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, address, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	return client.Do(req)
}

func TestAdapterTLSAuthenticatesBothPeers(t *testing.T) {
	t.Parallel()
	server, cfg := adapterTLSPeer(t)
	response, err := adapterTLSRequest(t, server.URL, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if closeErr := response.Body.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status=%d", response.StatusCode)
	}
}

func TestAdapterTLSRejectsUntrustedServer(t *testing.T) {
	t.Parallel()
	server, cfg := adapterTLSPeer(t)
	otherCert, otherKey := adapterTLSIdentity(t)
	other, err := newTLSConfig(otherCert, otherKey, otherCert)
	if err != nil {
		t.Fatal(err)
	}
	cfg.RootCAs = other.RootCAs
	response, err := adapterTLSRequest(t, server.URL, cfg)
	if response != nil {
		if closeErr := response.Body.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		t.Fatal("untrusted server returned a response")
	}
	var unknown x509.UnknownAuthorityError
	if !errors.As(err, &unknown) {
		t.Fatalf("error=%v, want unknown authority", err)
	}
}

func TestAdapterTLSRequiresClientIdentity(t *testing.T) {
	t.Parallel()
	server, cfg := adapterTLSPeer(t)
	cfg.Certificates = nil
	response, err := adapterTLSRequest(t, server.URL, cfg)
	if response != nil {
		if closeErr := response.Body.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		t.Fatal("unauthenticated client returned a response")
	}
	if err == nil {
		t.Fatal("mTLS accepted a client without an identity")
	}
}
