package config

import "github.com/kelseyhightower/envconfig"

// JobsConfig holds the jobs service configuration.
type JobsConfig struct {
	Environment

	Postgres
	ClickHouse
	Grpc
	Jobs
}

// Jobs holds the configuration for the jobs service.
type Jobs struct {
	FetchLimit     int `envconfig:"JOBS_FETCH_LIMIT" default:"10"`
	LogsFetchLimit int `envconfig:"JOBS_LOGS_FETCH_LIMIT" default:"100"`
}

// InitJobsServiceConfig initializes the jobs service configuration.
func InitJobsServiceConfig() (*JobsConfig, error) {
	var cfg JobsConfig
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
