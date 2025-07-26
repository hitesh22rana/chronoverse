package config

import (
	"time"

	"github.com/kelseyhightower/envconfig"
)

// JobLogsProcessor holds the job logs processor configuration.
type JobLogsProcessor struct {
	Environment

	Redis
	ClickHouse
	Kafka
	JobLogsProcessorConfig
}

// JobLogsProcessorConfig holds the configuration for the job logs processor.
type JobLogsProcessorConfig struct {
	BatchJobLogsSizeLimit      int           `envconfig:"JOBLOGS_PROCESSOR_BATCH_JOB_LOGS_SIZE_LIMIT" default:"1000"`
	BatchJobLogsTimeInterval   time.Duration `envconfig:"JOBLOGS_PROCESSOR_BATCH_JOB_LOGS_TIME_INTERVAL" default:"2s"`
	BatchAnalyticsTimeInterval time.Duration `envconfig:"JOBLOGS_PROCESSOR_BATCH_ANALYTICS_TIME_INTERVAL" default:"10s"`
}

// InitJobLogsProcessorConfig initializes the job logs processor configuration.
func InitJobLogsProcessorConfig() (*JobLogsProcessor, error) {
	var cfg JobLogsProcessor
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
