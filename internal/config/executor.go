package config

import (
	"github.com/kelseyhightower/envconfig"
)

// ExecutorConfig holds the executor configuration.
type ExecutorConfig struct {
	Environment

	Kafka
	WorkflowsService
	JobsService
	Executor
}

// Executor holds the configuration for the executor.
type Executor struct {
	ParallelismLimit int `envconfig:"EXECUTOR_PARALLELISM_LIMIT" default:"5"`
}

// InitExecutionJobConfig initializes the executor configuration.
func InitExecutionJobConfig() (*ExecutorConfig, error) {
	var cfg ExecutorConfig
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
