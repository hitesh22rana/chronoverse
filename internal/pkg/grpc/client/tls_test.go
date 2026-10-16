package client //nolint:testpackage // Exercises the private credential loader's transport contract.

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
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// One short-lived self-signed CA identity serves both ends of this local mTLS test.
func writeTLSIdentity(t *testing.T) (*TLSConfig, tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cert := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	dir := t.TempDir()
	cfg := &TLSConfig{Enabled: true, CAFile: filepath.Join(dir, "ca.pem"), ClientCertFile: filepath.Join(dir, "client.pem"), ClientKeyFile: filepath.Join(dir, "client.key")}
	for path, data := range map[string][]byte{cfg.CAFile: certPEM, cfg.ClientCertFile: certPEM, cfg.ClientKeyFile: keyPEM} {
		if writeErr := os.WriteFile(path, data, 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	identity, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("append test CA")
	}
	return cfg, identity, pool
}

func TestTLSCredentialInputErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		field    string
		contents string
		code     codes.Code
		message  string
	}{
		{"missing CA", "ca", "", codes.InvalidArgument, "failed to read CA certificate"},
		{"malformed CA", "ca", "not PEM", codes.Internal, "failed to append CA certificate"},
		{"missing client certificate", "cert", "", codes.InvalidArgument, "failed to load client certificate and key"},
		{"malformed client certificate", "cert", "not PEM", codes.InvalidArgument, "failed to load client certificate and key"},
		{"missing client key", "key", "", codes.InvalidArgument, "failed to load client certificate and key"},
		{"malformed client key", "key", "not PEM", codes.InvalidArgument, "failed to load client certificate and key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, _, _ := writeTLSIdentity(t)
			path := map[string]string{"ca": cfg.CAFile, "cert": cfg.ClientCertFile, "key": cfg.ClientKeyFile}[tt.field]
			if tt.contents == "" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte(tt.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			creds, err := loadTLSCredentials(cfg)
			if creds != nil || status.Code(err) != tt.code || !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("loadTLSCredentials = %v, %v; want nil, %v containing %q", creds, err, tt.code, tt.message)
			}
			conn, err := NewClient(&ServiceConfig{Host: "localhost", Port: 50051, TLS: cfg}, nil, nil)
			if conn != nil || status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "failed to load TLS credentials: rpc error: code = ") || !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("NewClient = %v, %v; want wrapped Internal credential error", conn, err)
			}
		})
	}
}

func TestNewClientMutualTLS(t *testing.T) {
	t.Parallel()
	cfg, identity, pool := writeTLSIdentity(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{ //nolint:gosec // Pin the test server to TLS1.2 to verify the client supports its minimum version.
		Certificates: []tls.Certificate{identity}, ClientCAs: pool,
		ClientAuth: tls.RequireAndVerifyClientCert,
		MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12,
	})))
	healthServer := health.NewServer()
	healthServer.SetServingStatus("tls-contract", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(server, healthServer)
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if serveErr := <-result; serveErr != nil {
			t.Errorf("Serve: %v", serveErr)
		}
	})
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := NewClient(&ServiceConfig{Host: "127.0.0.1", Port: portNumber, TLS: cfg}, nil, nil)
	if err != nil {
		t.Fatalf("NewClient with valid TLS identity: %v", err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var remote peer.Peer
	response, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{Service: "tls-contract"}, grpc.Peer(&remote))
	if err != nil {
		t.Fatalf("mutually authenticated health RPC: %v", err)
	}
	if response.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("health status = %v; want SERVING", response.GetStatus())
	}
	tlsInfo, ok := remote.AuthInfo.(credentials.TLSInfo)
	if !ok {
		t.Fatalf("authentication info = %T; want TLSInfo", remote.AuthInfo)
	}
	if tlsInfo.State.Version != tls.VersionTLS12 || len(tlsInfo.State.VerifiedChains) == 0 {
		t.Fatalf("handshake state = %+v; want TLS1.2 and verified server chain", tlsInfo.State)
	}
}
