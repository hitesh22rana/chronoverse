package meilisearch

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"os"

	"github.com/meilisearch/meilisearch-go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TLSConfig holds Meilisearch client TLS credentials.
type TLSConfig struct {
	Enabled                   bool
	CAFile, CertFile, KeyFile string
}

// Config represents the Meilisearch client configuration.
type Config struct {
	URI, MasterKey string
	TLSConfig      *TLSConfig
}

// New creates a Meilisearch client; request contexts bound individual operations.
func New(_ context.Context, cfg *Config) (meilisearch.ServiceManager, error) {
	if cfg == nil || cfg.URI == "" {
		return nil, status.Error(codes.InvalidArgument, "failed to initialize MeiliSearch client: missing uri")
	}
	if cfg.MasterKey == "" {
		return nil, status.Error(codes.InvalidArgument, "failed to initialize MeiliSearch client: missing masterkey")
	}
	opts := []meilisearch.Option{meilisearch.WithAPIKey(cfg.MasterKey)}
	if cfg.TLSConfig != nil && cfg.TLSConfig.Enabled {
		tlsConfig, err := newTLSConfig(cfg.TLSConfig.CertFile, cfg.TLSConfig.KeyFile, cfg.TLSConfig.CAFile)
		if err != nil {
			return nil, err
		}
		opts = append(opts, meilisearch.WithCustomClientWithTLS(tlsConfig))
	}
	return meilisearch.New(cfg.URI, opts...), nil
}

// newTLSConfig creates a new TLS config for the MeiliSearch client.
func newTLSConfig(certFile, keyFile, caFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to load client key pair: %v", err)
	}

	caCert, err := os.ReadFile(caFile)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to read CA certificate: %v", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, status.Error(codes.Internal, "failed to parse CA certificate")
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}
