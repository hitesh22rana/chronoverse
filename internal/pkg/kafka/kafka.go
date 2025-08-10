package kafka

import (
	"context"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// IsolationLevel represents the Kafka isolation level.
type IsolationLevel string

const (
	initTimeout time.Duration = 10 * time.Second

	// ReadUncommitted means that the consumer will read all messages, even those that are in the process of being written.
	ReadUncommitted IsolationLevel = "read_uncommitted"
	// ReadCommitted means that the consumer will only read messages that have been committed.
	ReadCommitted IsolationLevel = "read_committed"
)

// Config represents the configuration for a Kafka client.
type Config struct {
	Brokers             []string
	ConsumeTopics       []string
	ConsumerGroup       string
	TransactionalID     string
	FetchIsolationLevel IsolationLevel
	DisableAutoCommit   bool
}

// Option is a functional option type that allows us to configure the Kafka client.
type Option func(*Config)

// New creates a new Kafka client.
func New(ctx context.Context, options ...Option) (*kgo.Client, error) {
	_, cancel := context.WithTimeout(ctx, initTimeout)
	defer cancel()

	c := &Config{}

	for _, opt := range options {
		opt(c)
	}

	if len(c.Brokers) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "failed to initialize Kafka client: missing brokers")
	}

	opts := []kgo.Opt{
		kgo.SeedBrokers(c.Brokers...),
		kgo.AllowAutoTopicCreation(),
	}

	if len(c.ConsumeTopics) != 0 {
		opts = append(opts, kgo.ConsumeTopics(c.ConsumeTopics...))
	}

	if c.ConsumerGroup != "" {
		opts = append(opts, kgo.ConsumerGroup(c.ConsumerGroup))
	}

	if c.TransactionalID != "" {
		opts = append(opts, kgo.TransactionalID(c.TransactionalID))
	}

	if c.FetchIsolationLevel != "" {
		// Default to read uncommitted if not set
		var fetchIsolationLevel kgo.IsolationLevel
		if c.FetchIsolationLevel == ReadCommitted {
			fetchIsolationLevel = kgo.ReadCommitted()
		} else {
			fetchIsolationLevel = kgo.ReadUncommitted()
		}

		opts = append(opts, kgo.FetchIsolationLevel(fetchIsolationLevel))
	}

	if c.DisableAutoCommit {
		opts = append(opts, kgo.DisableAutoCommit())
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

// WithTransactionalID sets the Kafka transactional ID.
func WithTransactionalID(id string) Option {
	return func(c *Config) {
		c.TransactionalID = id
	}
}

// WithFetchIsolationLevel sets the Kafka fetch isolation level.
func WithFetchIsolationLevel(isolationLevel IsolationLevel) Option {
	return func(c *Config) {
		c.FetchIsolationLevel = isolationLevel
	}
}

// WithDisableAutoCommit disables the Kafka auto commit.
func WithDisableAutoCommit() Option {
	return func(c *Config) {
		c.DisableAutoCommit = true
	}
}
