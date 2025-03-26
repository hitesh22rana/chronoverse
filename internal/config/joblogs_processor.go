package config

import (
	"time"

	"github.com/kelseyhightower/envconfig"
)

// JobLogsProcessor holds the job logs processor configuration.
type JobLogsProcessor struct {
	Environment

	ClickHouse
	Kafka
	JobLogsProcessorConfig
}

// JobLogsProcessorConfig holds the configuration for the job logs processor.
type JobLogsProcessorConfig struct {
	BatchSizeLimit int           `envconfig:"JOBLOGS_PROCESSOR_BATCH_SIZE" default:"1000"`
	BatchTimeLimit time.Duration `envconfig:"JOBLOGS_PROCESSOR_BATCH_TIME_LIMIT" default:"2s"`
}

// InitJobLogsProcessorConfig initializes the job logs processor configuration.
func InitJobLogsProcessorConfig() (*JobLogsProcessor, error) {
	var cfg JobLogsProcessor
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
