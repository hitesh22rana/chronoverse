package config

import "github.com/kelseyhightower/envconfig"

// WorkflowWorker holds the workflow worker configuration.
type WorkflowWorker struct {
	Environment

	Redis
	ClickHouse
	ClientTLS
	Kafka
	WorkflowsService
	JobsService
	NotificationsService
	WorkflowWorkerConfig
}

// WorkflowWorkerConfig holds the configuration for the workflow worker.
type WorkflowWorkerConfig struct{}

// InitWorkflowWorkerConfig initializes the workflow worker configuration.
func InitWorkflowWorkerConfig() (*WorkflowWorker, error) {
	var cfg WorkflowWorker
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
