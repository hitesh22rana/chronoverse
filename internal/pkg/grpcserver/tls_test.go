package grpcserver_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	grpcserverpkg "github.com/hitesh22rana/chronoverse/internal/pkg/grpcserver"
)

func TestLoadTLSConfig(t *testing.T) {
	t.Parallel()
	certFile, keyFile := writeTLSCertificate(t)
	cfg, err := grpcserverpkg.LoadTLSConfig(certFile, certFile, keyFile)
	require.NoError(t, err)
	require.Equal(t, uint16(tls.VersionTLS12), cfg.MinVersion)

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = cfg
	server.StartTLS()
	t.Cleanup(server.Close)

	otherCertFile, otherKeyFile := writeTLSCertificate(t)
	untrusted, err := tls.LoadX509KeyPair(otherCertFile, otherKeyFile)
	require.NoError(t, err)
	for _, tc := range []struct {
		name      string
		certs     []tls.Certificate
		wantError bool
	}{
		{name: "trusted", certs: cfg.Certificates},
		{name: "missing", wantError: true},
		{name: "untrusted", certs: []tls.Certificate{untrusted}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			transport := &http.Transport{TLSClientConfig: &tls.Config{
				RootCAs:    cfg.ClientCAs,
				ServerName: "localhost",
				MinVersion: tls.VersionTLS12,
				// Always send the chosen certificate, even when it isn't in the server's CA list.
				GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
					if len(tc.certs) == 0 {
						return &tls.Certificate{}, nil
					}
					return &tc.certs[0], nil
				},
			}}
			t.Cleanup(transport.CloseIdleConnections)
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, http.NoBody)
			require.NoError(t, err)
			res, err := client.Do(req)
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, http.StatusNoContent, res.StatusCode)
		})
	}

	invalidFile := filepath.Join(t.TempDir(), "invalid.pem")
	require.NoError(t, os.WriteFile(invalidFile, []byte("not a certificate"), 0o600))
	for _, paths := range [][3]string{
		{invalidFile + ".missing", certFile, keyFile},
		{invalidFile, certFile, keyFile},
		{certFile, invalidFile, keyFile},
		{certFile, certFile, otherKeyFile},
	} {
		_, err := grpcserverpkg.LoadTLSConfig(paths[0], paths[1], paths[2])
		require.Error(t, err)
	}
}

func writeTLSCertificate(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1), DNSNames: []string{"localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage:    x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	require.NoError(t, os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600))
	return certFile, keyFile
}
