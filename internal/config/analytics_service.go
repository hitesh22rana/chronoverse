package config

import (
	"github.com/kelseyhightower/envconfig"
)

// AnalyticsConfig holds the configuration for the analytics service.
type AnalyticsConfig struct {
	Environment

	Grpc
	Postgres
	AnalyticsServiceConfig
}

// AnalyticsServiceConfig holds the configuration for the analytics service.
type AnalyticsServiceConfig struct{}

// InitAnalyticsServiceConfig initializes the analytics service configuration.
func InitAnalyticsServiceConfig() (*AnalyticsConfig, error) {
	var cfg AnalyticsConfig
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
