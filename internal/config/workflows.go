package config

import "github.com/kelseyhightower/envconfig"

// WorkflowsConfig holds the workflows service configuration.
type WorkflowsConfig struct {
	Environment

	Postgres
	Grpc
	Kafka
	Workflows
}

// Workflows holds the configuration for the workflows service.
type Workflows struct {
	FetchLimit int `envconfig:"WORKFLOWS_FETCH_LIMIT" default:"10"`
}

// InitWorkflowsServiceConfig initializes the workflows service configuration.
func InitWorkflowsServiceConfig() (*WorkflowsConfig, error) {
	var cfg WorkflowsConfig
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
