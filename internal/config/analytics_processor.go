package config

import "github.com/kelseyhightower/envconfig"

// AnalyticsProcessor holds the analytics processor configuration.
type AnalyticsProcessor struct {
	Environment

	Postgres
	Kafka
	AnalyticsProcessorConfig
}

// AnalyticsProcessorConfig holds the configuration for the analytics processor.
type AnalyticsProcessorConfig struct{}

// InitAnalyticsProcessorConfig initializes the analytics processor configuration.
func InitAnalyticsProcessorConfig() (*AnalyticsProcessor, error) {
	var cfg AnalyticsProcessor
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
