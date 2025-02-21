package config

import "github.com/kelseyhightower/envconfig"

// JobsConfig holds the jobs service configuration.
type JobsConfig struct {
	Configuration

	Postgres
	Grpc
}

// InitJobsServiceConfig initializes the jobs service configuration.
func InitJobsServiceConfig() (*JobsConfig, error) {
	var cfg JobsConfig
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
