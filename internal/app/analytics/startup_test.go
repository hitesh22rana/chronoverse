package analytics_test

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
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	gootelog "go.opentelemetry.io/otel/sdk/log"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	reflectionpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/app/analytics"
	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
)

func TestAnalyticsHealthAndReflection(t *testing.T) {
	for _, tc := range []struct {
		environment string
		reflection  bool
	}{
		{"development", true},
		{"PrOdUcTiOn", false},
	} {
		t.Run(tc.environment, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			// No auth expectation: infrastructure health checks must work without a JWT.
			auth := authmock.NewMockIAuth(gomock.NewController(t))
			server := analytics.New(ctx, &analytics.Config{Environment: tc.environment}, auth, nil)
			conn := initClient(t, server, insecure.NewCredentials())
			health, err := grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
			require.NoError(t, err)
			require.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, health.GetStatus())

			stream, err := reflectionpb.NewServerReflectionClient(conn).ServerReflectionInfo(ctx)
			require.NoError(t, err)
			sendErr := stream.Send(&reflectionpb.ServerReflectionRequest{
				MessageRequest: &reflectionpb.ServerReflectionRequest_ListServices{ListServices: ""},
			})
			response, err := stream.Recv()
			require.NoError(t, stream.CloseSend())
			if !tc.reflection {
				require.Equal(t, codes.Unimplemented, status.Code(err))
				require.Nil(t, response)
				return
			}
			require.NoError(t, sendErr)
			require.NoError(t, err)
			var services []string
			for _, service := range response.GetListServicesResponse().GetService() {
				services = append(services, service.GetName())
			}
			require.Contains(t, services, "analytics.AnalyticsService")
			require.Contains(t, services, "grpc.health.v1.Health")
		})
	}
}

func TestAnalyticsTLS(t *testing.T) {
	cfg, trusted := analyticsTLSCertificate(t)
	for _, tc := range []struct {
		name      string
		transport credentials.TransportCredentials
		want      codes.Code
	}{
		{"trusted client", credentials.NewTLS(trusted), codes.OK},
		{"missing client certificate", credentials.NewTLS(&tls.Config{
			RootCAs: trusted.RootCAs, ServerName: "localhost", MinVersion: tls.VersionTLS12,
		}), codes.Unavailable},
		{"plaintext", insecure.NewCredentials(), codes.Unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			server := analytics.New(ctx, &analytics.Config{TLSConfig: cfg}, nil, nil)
			conn := initClient(t, server, tc.transport)
			response, err := grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
			require.Equal(t, tc.want, status.Code(err), "%v", err)
			if tc.want == codes.OK {
				require.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, response.GetStatus())
			} else {
				require.Nil(t, response)
			}
		})
	}
}

func TestAnalyticsRejectsInvalidTLS(t *testing.T) {
	const child = "CHRONOVERSE_TEST_ANALYTICS_INVALID_TLS"
	if os.Getenv(child) == "1" {
		ctx, _ := loggerpkg.Init(t.Context(), "analytics-test", gootelog.NewLoggerProvider())
		server := analytics.New(ctx, &analytics.Config{TLSConfig: &analytics.TLSConfig{Enabled: true}}, nil, nil)
		if server != nil {
			server.Stop()
		}
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	executable, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestAnalyticsRejectsInvalidTLS$")
	cmd.Env = append(os.Environ(), child+"=1")
	if directory := flag.Lookup("test.gocoverdir").Value.String(); directory != "" {
		// Preserve the exiting child's counters in the parent test's coverage profile.
		cmd.Env = append(cmd.Env, "GOCOVERDIR="+directory)
	}
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	require.Equal(t, 1, exit.ExitCode(), "%s", output)
	require.Contains(t, string(output), "failed to load TLS credentials")
	require.Contains(t, string(output), "failed to read CA certificate file")
}

func analyticsTLSCertificate(t *testing.T) (server *analytics.TLSConfig, client *tls.Config) {
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
	return &analytics.TLSConfig{Enabled: true, CAFile: certFile, CertFile: certFile, KeyFile: keyFile}, &tls.Config{
		Certificates: []tls.Certificate{identity}, RootCAs: roots, ServerName: "localhost", MinVersion: tls.VersionTLS12,
	}
}
