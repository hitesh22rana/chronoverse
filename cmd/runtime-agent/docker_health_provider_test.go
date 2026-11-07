package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hitesh22rana/chronoverse/internal/config"
)

func healthIdentity(t *testing.T) (certFile, keyFile string, identity tls.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1), DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	require.NoError(t, err)
	private, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private})
	certFile, keyFile = filepath.Join(t.TempDir(), "cert.pem"), filepath.Join(t.TempDir(), "key.pem")
	require.NoError(t, os.WriteFile(certFile, certPEM, 0o600))
	require.NoError(t, os.WriteFile(keyFile, keyPEM, 0o600))
	identity, err = tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)
	return certFile, keyFile, identity
}

func TestDockerHealthProviderUsesHealthEndpointAndCredentials(t *testing.T) {
	t.Setenv("DOCKER_TLS_VERIFY", "")
	t.Setenv("DOCKER_CERT_PATH", "")
	t.Setenv("DOCKER_API_VERSION", "")
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "plaintext", true: "mutual TLS"}[secure], func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/_ping") {
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if r.Header.Get("X-Chronoverse-Docker-Proxy-Token") != "test-token" {
					t.Error("missing configured proxy token")
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				if secure && (r.TLS == nil || len(r.TLS.VerifiedChains) == 0) {
					t.Error("unverified client certificate")
					w.WriteHeader(http.StatusForbidden)
					return
				}
				requests.Add(1)
				w.Header().Set("Api-Version", "1.51")
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			cfg := &config.RuntimeAgent{}
			cfg.RuntimeAgentConfig.DockerEndpoint = "tcp://advertised.invalid:2376"
			cfg.DockerProxy.Token = "test-token"
			if secure {
				cert, key, identity := healthIdentity(t)
				roots := x509.NewCertPool()
				contents, err := os.ReadFile(cert)
				require.NoError(t, err)
				require.True(t, roots.AppendCertsFromPEM(contents))
				server.TLS = &tls.Config{Certificates: []tls.Certificate{identity}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}
				server.StartTLS()
				cfg.DockerProxy.TLS.CAFile = cert
				cfg.DockerProxy.TLS.CertFile = cert
				cfg.DockerProxy.TLS.KeyFile = key
				cfg.DockerProxy.TLS.ServerName = "localhost"
			} else {
				server.Start()
			}
			cfg.RuntimeAgentConfig.DockerHealthEndpoint = "tcp://" + strings.TrimPrefix(strings.TrimPrefix(server.URL, "http://"), "https://")
			provider, err := newDockerHealthProvider(cfg)
			require.NoError(t, err)
			defer func() { require.NoError(t, provider.Close()) }()
			require.NoError(t, provider.Healthy(t.Context()))
			require.Positive(t, requests.Load())
		})
	}
}

func TestDockerHealthProviderRejectsMisconfiguredTLS(t *testing.T) {
	cfg := &config.RuntimeAgent{}
	cfg.RuntimeAgentConfig.DockerHealthEndpoint = "tcp://127.0.0.1:1"
	cfg.DockerProxy.TLS.CAFile = "missing-ca.pem"
	provider, err := newDockerHealthProvider(cfg)
	require.Nil(t, provider)
	require.ErrorContains(t, err, "TLS misconfigured")
}
