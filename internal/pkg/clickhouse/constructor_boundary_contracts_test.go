//nolint:testpackage // Verifies constructor TLS handling before any live database access.
package clickhouse

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestConstructorRejectsTLSFilesAndCanceledHealth(t *testing.T) {
	cert, key := adapterTLSIdentity(t)
	for _, tc := range []struct {
		name    string
		tls     TLSConfig
		message string
	}{
		{"invalid TLS", TLSConfig{Enabled: true, CertFile: "missing", KeyFile: "missing", CAFile: "missing"}, "client key pair"},
		{"valid TLS canceled", TLSConfig{Enabled: true, CertFile: cert, KeyFile: key, CAFile: cert}, "initial health check failed"},
		{"plain canceled", TLSConfig{}, "initial health check failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			client, err := New(ctx, &Config{Hosts: []string{"localhost:9000"}, TLSConfig: &tc.tls})
			require.Nil(t, client)
			require.Equal(t, codes.Internal, status.Code(err))
			require.ErrorContains(t, err, tc.message)
		})
	}
}
