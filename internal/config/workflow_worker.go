package config

import "github.com/kelseyhightower/envconfig"

// WorkflowWorker holds the workflow worker configuration.
type WorkflowWorker struct {
	Environment

	Kafka
	WorkflowsService
	JobsService
	WorkflowWorkerConfig
}

// WorkflowWorkerConfig holds the configuration for the workflow worker.
type WorkflowWorkerConfig struct {
	ParallelismLimit int `envconfig:"WORKFLOW_WORKER_CONFIG_PARALLELISM_LIMIT" default:"5"`
}

// InitWorkflowWorkerConfig initializes the workflow worker configuration.
func InitWorkflowWorkerConfig() (*WorkflowWorker, error) {
	var cfg WorkflowWorker
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
