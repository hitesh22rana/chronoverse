package config

import (
	"time"

	"github.com/kelseyhightower/envconfig"
)

// SchedulerConfig holds the scheduler configuration.
type SchedulerConfig struct {
	Configuration

	Postgres
	Scheduler
}

// Scheduler holds the configuration for the scheduler.
type Scheduler struct {
	PollInterval time.Duration `envconfig:"SCHEDULER_POLL_INTERVAL" default:"10s"`
}

// InitSchedulerConfig initializes the scheduler configuration.
func InitSchedulerConfig() (*SchedulerConfig, error) {
	var cfg SchedulerConfig
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
