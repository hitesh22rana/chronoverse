//nolint:testpackage // Exercises the TLS adapter using a real mutual-TLS connection.
package meilisearch

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestClientConfigRejectsMissingFieldsAndTLSFiles(t *testing.T) {
	for _, cfg := range []*Config{nil, {}, {URI: "http://search.test"}} {
		client, err := New(t.Context(), cfg)
		require.Nil(t, client)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	}
	client, err := New(t.Context(), &Config{URI: "https://search.test", MasterKey: "public-test-key", TLSConfig: &TLSConfig{Enabled: true, CertFile: "missing", KeyFile: "missing", CAFile: "missing"}})
	require.Nil(t, client)
	require.Equal(t, codes.Internal, status.Code(err))
	require.ErrorContains(t, err, "client key pair")
}

func TestClientConfigAppliesCredentialsToPlainAndTLSRequests(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "plaintext", true: "mutual TLS"}[secure], func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/health" || r.Header.Get("Authorization") != "Bearer public-test-key" {
					t.Error("request lost endpoint or API key")
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				if secure && (r.TLS == nil || len(r.TLS.VerifiedChains) == 0) {
					t.Error("unverified client")
					w.WriteHeader(http.StatusForbidden)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]string{"status": "available"}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			cfg := &Config{MasterKey: "public-test-key"}
			if secure {
				cert, key := adapterTLSIdentity(t)
				tlsConfig, err := newTLSConfig(cert, key, cert)
				require.NoError(t, err)
				server.TLS = tlsConfig.Clone()
				server.TLS.ClientCAs = tlsConfig.RootCAs
				server.TLS.ClientAuth = tls.RequireAndVerifyClientCert
				server.StartTLS()
				cfg.TLSConfig = &TLSConfig{Enabled: true, CertFile: cert, KeyFile: key, CAFile: cert}
			} else {
				server.Start()
				cfg.TLSConfig = &TLSConfig{Enabled: false, CAFile: "unused-missing-file"}
			}
			cfg.URI = server.URL
			client, err := New(t.Context(), cfg)
			require.NoError(t, err)
			defer client.Close()
			health, err := client.HealthWithContext(t.Context())
			require.NoError(t, err)
			require.Equal(t, "available", health.Status)
		})
	}
	cfg := &Config{URI: "http://search.test", MasterKey: "public-test-key"}
	client, err := New(t.Context(), cfg)
	require.NoError(t, err)
	client.Close()
}
