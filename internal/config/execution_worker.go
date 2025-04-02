package config

import (
	"github.com/kelseyhightower/envconfig"
)

// ExecutionWorker holds the execution worker configuration.
type ExecutionWorker struct {
	Environment

	Kafka
	WorkflowsService
	JobsService
	NotificationsService
	ExecutionWorkerConfig
}

// ExecutionWorkerConfig holds the configuration for the execution worker.
type ExecutionWorkerConfig struct {
	ParallelismLimit int `envconfig:"EXECUTION_WORKER_PARALLELISM_LIMIT" default:"5"`
}

// InitExecutionJobConfig initializes the execution worker configuration.
func InitExecutionJobConfig() (*ExecutionWorker, error) {
	var cfg ExecutionWorker
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
