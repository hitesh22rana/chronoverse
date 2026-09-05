package kafka

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"os"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/config"
)

// Config represents the configuration for a Kafka client.
type Config struct {
	Brokers            []string
	ConsumeTopics      []string
	ConsumerGroup      string
	DisableAutoCommit  bool
	PartitionLifecycle *PartitionLifecycle
	TLS                *tls.Config
}

// Option is a functional option type that allows to configure the Kafka client.
type Option func(*Config)

// New creates a new Kafka client.
func New(_ context.Context, options ...Option) (*kgo.Client, error) {
	c := &Config{}

	for _, opt := range options {
		opt(c)
	}

	if len(c.Brokers) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "failed to initialize Kafka client: missing brokers")
	}

	opts := []kgo.Opt{
		kgo.SeedBrokers(c.Brokers...),
	}

	if c.TLS != nil {
		opts = append(opts, kgo.DialTLSConfig(c.TLS))
	}

	if len(c.ConsumeTopics) != 0 {
		opts = append(opts, kgo.ConsumeTopics(c.ConsumeTopics...))
	}

	if c.ConsumerGroup != "" {
		opts = append(opts, kgo.ConsumerGroup(c.ConsumerGroup))
	}

	if c.DisableAutoCommit {
		opts = append(opts, kgo.DisableAutoCommit())
	}
	if c.PartitionLifecycle != nil {
		opts = append(opts,
			kgo.OnPartitionsAssigned(c.PartitionLifecycle.OnAssigned),
			kgo.OnPartitionsRevoked(c.PartitionLifecycle.OnRevoked),
			kgo.OnPartitionsLost(c.PartitionLifecycle.OnLost),
		)
	}

	return kgo.NewClient(opts...)
}

// WithBrokers sets the Kafka brokers.
func WithBrokers(brokers ...string) Option {
	return func(c *Config) {
		c.Brokers = brokers
	}
}

// WithConsumeTopics sets the Kafka consume topic.
func WithConsumeTopics(topic ...string) Option {
	return func(c *Config) {
		c.ConsumeTopics = topic
	}
}

// WithConsumerGroup sets the Kafka consumer group.
func WithConsumerGroup(group string) Option {
	return func(c *Config) {
		c.ConsumerGroup = group
	}
}

// WithDisableAutoCommit disables the Kafka auto commit.
func WithDisableAutoCommit() Option {
	return func(c *Config) {
		c.DisableAutoCommit = true
	}
}

// WithPartitionLifecycle sets the kafka partition lifecycle callbacks.
func WithPartitionLifecycle(lifecycle *PartitionLifecycle) Option {
	return func(c *Config) {
		c.PartitionLifecycle = lifecycle
	}
}

// WithTLS sets the Kafka TLS config.
func WithTLS(cfg *config.Kafka) Option {
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

// newTLSConfig creates a new TLS config for the Kafka client.
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
