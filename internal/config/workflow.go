package config

import "github.com/kelseyhightower/envconfig"

// WorkflowConfig holds the workflow service configuration.
type WorkflowConfig struct {
	Environment

	Kafka
	JobsService
	Workflow
}

// Workflow holds the configuration for the workflow service.
type Workflow struct {
	ParallelismLimit int `envconfig:"WORKFLOW_PARALLELISM_LIMIT" default:"5"`
}

// InitWorkflowServiceConfig initializes the workflow service configuration.
func InitWorkflowServiceConfig() (*WorkflowConfig, error) {
	var cfg WorkflowConfig
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
