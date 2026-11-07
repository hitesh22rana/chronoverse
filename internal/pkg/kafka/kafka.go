package kafka

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"os"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TLSConfig holds Kafka client TLS credentials.
type TLSConfig struct {
	Enabled                   bool
	CAFile, CertFile, KeyFile string
}

// Config represents the configuration for a Kafka client.
type Config struct {
	Brokers            []string
	ConsumeTopics      []string
	ConsumerGroup      string
	DisableAutoCommit  bool
	PartitionLifecycle *PartitionLifecycle
	TLSConfig          *TLSConfig
}

// Option is a functional option type that allows to configure the Kafka client.
type Option func(*Config)

// New creates a new Kafka client.
func New(_ context.Context, cfg *Config, options ...Option) (*kgo.Client, error) {
	if cfg == nil {
		return nil, status.Error(codes.InvalidArgument, "failed to initialize Kafka client: missing brokers")
	}
	settings := *cfg
	c := &settings

	for _, opt := range options {
		opt(c)
	}

	if len(c.Brokers) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "failed to initialize Kafka client: missing brokers")
	}

	opts := []kgo.Opt{
		kgo.SeedBrokers(c.Brokers...),
	}

	if c.TLSConfig != nil && c.TLSConfig.Enabled {
		tlsConfig, err := newTLSConfig(c.TLSConfig.CertFile, c.TLSConfig.KeyFile, c.TLSConfig.CAFile)
		if err != nil {
			return nil, err
		}
		opts = append(opts, kgo.DialTLSConfig(tlsConfig))
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
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, status.Error(codes.Internal, "failed to parse CA certificate")
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}
