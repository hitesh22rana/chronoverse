package meilisearch

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"os"
	"time"

	"github.com/meilisearch/meilisearch-go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/config"
)

const (
	initTimeout time.Duration = 10 * time.Second
)

// Config represents the configuration for the MeiliSearch client.
type Config struct {
	URI       string
	MasterKey string
	TLS       *tls.Config
}

// Option is a functional option type that allows us to configure the MeiliSearch client.
type Option func(*Config)

// New creates a new MeiliSearch client.
func New(ctx context.Context, options ...Option) (meilisearch.ServiceManager, error) {
	_, cancel := context.WithTimeout(ctx, initTimeout)
	defer cancel()

	c := &Config{}

	for _, opt := range options {
		opt(c)
	}

	opts := []meilisearch.Option{}
	if c.URI == "" {
		return nil, status.Errorf(codes.InvalidArgument, "failed to initialize MeiliSearch client: missing uri")
	}
	if c.MasterKey == "" {
		return nil, status.Errorf(codes.InvalidArgument, "failed to initialize MeiliSearch client: missing masterkey")
	}

	opts = append(opts, meilisearch.WithAPIKey(c.MasterKey))
	if c.TLS != nil {
		opts = append(opts, meilisearch.WithCustomClientWithTLS(c.TLS))
	}

	return meilisearch.New(c.URI, opts...), nil
}

// WithURI sets the MeiliSearch uri config.
func WithURI(uri string) Option {
	return func(c *Config) {
		if uri == "" {
			return
		}

		c.URI = uri
	}
}

// WithMasterKey sets the MeiliSearch masterkey config.
func WithMasterKey(masterKey string) Option {
	return func(c *Config) {
		if masterKey == "" {
			return
		}

		c.MasterKey = masterKey
	}
}

// WithTLS sets the MeiliSearch TLS config.
func WithTLS(cfg *config.MeiliSearch) Option {
	return func(c *Config) {
		if !cfg.TLS.Enabled {
			return
		}

		tlsConfig, err := newTLSConfig(cfg.TLS.CertFile, cfg.TLS.KeyFile, cfg.TLS.CAFile)
		if err != nil {
			return
		}
		c.TLS = tlsConfig
	}
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
	caCertPool.AppendCertsFromPEM(caCert)

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}
