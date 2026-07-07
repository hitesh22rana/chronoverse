package config

import (
	"time"

	"github.com/kelseyhightower/envconfig"
)

// JobsConfig holds the jobs service configuration.
type JobsConfig struct {
	Environment

	Grpc
	Postgres
	Redis
	ClickHouse
	MeiliSearch
	ClientTLS
	WorkflowsService
	JobsServiceConfig
}

// JobsServiceConfig holds the configuration for the jobs service.
type JobsServiceConfig struct {
	FetchLimit          int           `envconfig:"JOBS_SERVICE_CONFIG_FETCH_LIMIT" default:"10"`
	LogsFetchLimit      int           `envconfig:"JOBS_SERVICE_CONFIG_LOGS_FETCH_LIMIT" default:"100"`
	RuntimeHeartbeatTTL time.Duration `envconfig:"JOBS_SERVICE_RUNTIME_HEARTBEAT_TTL" default:"30s"`
	RuntimeLostAfter    time.Duration `envconfig:"JOBS_SERVICE_RUNTIME_LOST_AFTER" default:"5m"`
}

// InitJobsServiceConfig initializes the jobs service configuration.
func InitJobsServiceConfig() (*JobsConfig, error) {
	var cfg JobsConfig
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
