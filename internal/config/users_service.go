package config

import (
	"github.com/kelseyhightower/envconfig"
)

// UsersConfig holds the configuration for the users service.
type UsersConfig struct {
	Environment

	Grpc
	Postgres
}

// InitUsersServiceConfig initializes the users service configuration.
func InitUsersServiceConfig() (*UsersConfig, error) {
	var cfg UsersConfig
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
