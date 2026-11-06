package notifications_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	grpc_health_v1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"

	"github.com/hitesh22rana/chronoverse/internal/app/notifications"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
)

func TestNotificationsServerLoadsMutualTLS(t *testing.T) {
	cfg, clientTLS := notificationsContractTLSCertificate(t)
	server := notifications.New(t.Context(), &notifications.Config{Environment: "production", TLSConfig: cfg}, nil, nil)
	listener := bufconn.Listen(1024 * 1024)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); require.NoError(t, listener.Close()); require.NoError(t, <-done) })
	conn,
		err := grpc.NewClient("passthrough:///tls-notifications-contract",
		grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)),
		grpc.WithContextDialer(func(ctx context.Context,
			_ string) (net.Conn,
			error,
		) {
			return listener.DialContext(ctx)
		}))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	response, err := grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err)
	require.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, response.GetStatus())
}

func TestNotificationsServerRejectsInvalidTLS(t *testing.T) {
	const child = "CHRONOVERSE_TEST_NOTIFICATIONS_INVALID_TLS"
	if os.Getenv(child) == "1" {
		ctx, _ := loggerpkg.Init(t.Context(), "notifications-tls-contract", sdklog.NewLoggerProvider())
		notifications.New(ctx, &notifications.Config{TLSConfig: &notifications.TLSConfig{Enabled: true}}, nil, nil)
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	executable, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestNotificationsServerRejectsInvalidTLS$")
	cmd.Env = append(os.Environ(), child+"=1")
	if directory := flag.Lookup("test.gocoverdir").Value.String(); directory != "" {
		cmd.Env = append(cmd.Env, "GOCOVERDIR="+directory)
	}
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	require.Equal(t, 1, exit.ExitCode(), "%s", output)
	require.Contains(t, string(output), "failed to load TLS credentials")
	require.Contains(t, string(output), "failed to read CA certificate file")
}

func notificationsContractTLSCertificate(t *testing.T) (server *notifications.TLSConfig, client *tls.Config) {
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
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	require.NoError(t, os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600))
	identity, err := tls.LoadX509KeyPair(certFile, keyFile)
	require.NoError(t, err)
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})))
	return &notifications.TLSConfig{Enabled: true, CAFile: certFile, CertFile: certFile, KeyFile: keyFile}, &tls.Config{
		Certificates: []tls.Certificate{identity}, RootCAs: roots, ServerName: "localhost", MinVersion: tls.VersionTLS12,
	}
}
