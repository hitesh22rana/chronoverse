package config

import (
	"time"

	"github.com/kelseyhightower/envconfig"
)

// WorkflowsConfig holds the workflows service configuration.
type WorkflowsConfig struct {
	Environment

	Grpc
	Postgres
	Redis
	WorkflowsServiceConfig
}

// WorkflowsServiceConfig holds the configuration for the workflows service.
type WorkflowsServiceConfig struct {
	FetchLimit       int           `envconfig:"WORKFLOWS_SERVICE_CONFIG_FETCH_LIMIT" default:"10"`
	CleanupEnabled   bool          `envconfig:"WORKFLOWS_CLEANUP_ENABLED" default:"true"`
	CleanupInterval  time.Duration `envconfig:"WORKFLOWS_CLEANUP_INTERVAL" default:"15m"`
	CleanupBatchSize int           `envconfig:"WORKFLOWS_CLEANUP_BATCH_SIZE" default:"1000"`
}

// InitWorkflowsServiceConfig initializes the workflows service configuration.
func InitWorkflowsServiceConfig() (*WorkflowsConfig, error) {
	var cfg WorkflowsConfig
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
