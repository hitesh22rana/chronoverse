package config

import (
	"time"

	"github.com/kelseyhightower/envconfig"
)

// AnalyticsProcessor holds the analytics processor configuration.
type AnalyticsProcessor struct {
	Environment

	Postgres
	Kafka
	AnalyticsProcessorConfig
}

// AnalyticsProcessorConfig holds the configuration for the analytics processor.
type AnalyticsProcessorConfig struct {
	CleanupEnabled           bool          `envconfig:"ANALYTICS_PROCESSOR_CLEANUP_ENABLED" default:"true"`
	CleanupInterval          time.Duration `envconfig:"ANALYTICS_PROCESSOR_CLEANUP_INTERVAL" default:"15m"`
	CleanupBatchSize         int           `envconfig:"ANALYTICS_PROCESSOR_CLEANUP_BATCH_SIZE" default:"1000"`
	ProcessedEventsRetention time.Duration `envconfig:"ANALYTICS_PROCESSED_EVENTS_RETENTION" default:"336h"`
}

// InitAnalyticsProcessorConfig initializes the analytics processor configuration.
func InitAnalyticsProcessorConfig() (*AnalyticsProcessor, error) {
	var cfg AnalyticsProcessor
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
