package config

import (
	"github.com/kelseyhightower/envconfig"
)

// ExecutorConfig holds the executor configuration.
type ExecutorConfig struct {
	Environment

	Executor
	Kafka
	JobsService
}

// Executor holds the configuration for the executor.
type Executor struct{}

// InitExecutionServiceConfig initializes the executor configuration.
func InitExecutionServiceConfig() (*ExecutorConfig, error) {
	var cfg ExecutorConfig
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
