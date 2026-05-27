package config

import (
	"time"

	"github.com/kelseyhightower/envconfig"
)

// OutboxRelay holds the outbox relay configuration.
type OutboxRelay struct {
	Environment

	Postgres
	Kafka
	OutboxRelayConfig
}

// OutboxRelayConfig holds the configuration for outbox relay handlers.
type OutboxRelayConfig struct {
	WorkflowEnabled    bool          `envconfig:"OUTBOX_RELAY_WORKFLOW_ENABLED" default:"true"`
	JobsEnabled        bool          `envconfig:"OUTBOX_RELAY_JOBS_ENABLED" default:"true"`
	JobLogsEnabled     bool          `envconfig:"OUTBOX_RELAY_JOB_LOGS_ENABLED" default:"true"`
	AnalyticsEnabled   bool          `envconfig:"OUTBOX_RELAY_ANALYTICS_ENABLED" default:"true"`
	BatchSize          int           `envconfig:"OUTBOX_RELAY_BATCH_SIZE" default:"100"`
	PollInterval       time.Duration `envconfig:"OUTBOX_RELAY_POLL_INTERVAL" default:"1s"`
	ContextTimeout     time.Duration `envconfig:"OUTBOX_RELAY_CONTEXT_TIMEOUT" default:"10s"`
	MaxAttempts        int           `envconfig:"OUTBOX_RELAY_MAX_ATTEMPTS" default:"10"`
	RetryBackoff       time.Duration `envconfig:"OUTBOX_RELAY_RETRY_BACKOFF" default:"5s"`
	ProcessingLease    time.Duration `envconfig:"OUTBOX_RELAY_PROCESSING_LEASE" default:"30s"`
	WorkerID           string        `envconfig:"OUTBOX_RELAY_WORKER_ID" default:"outbox-relay"`
	CleanupEnabled     bool          `envconfig:"OUTBOX_RELAY_CLEANUP_ENABLED" default:"true"`
	CleanupInterval    time.Duration `envconfig:"OUTBOX_RELAY_CLEANUP_INTERVAL" default:"15m"`
	CleanupBatchSize   int           `envconfig:"OUTBOX_RELAY_CLEANUP_BATCH_SIZE" default:"1000"`
	PublishedRetention time.Duration `envconfig:"OUTBOX_RELAY_PUBLISHED_RETENTION" default:"168h"`
}

// InitOutboxRelayConfig initializes the outbox relay configuration.
func InitOutboxRelayConfig() (*OutboxRelay, error) {
	var cfg OutboxRelay
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
