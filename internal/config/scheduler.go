package config

import (
	"time"

	"github.com/kelseyhightower/envconfig"
)

// SchedulerConfig holds the scheduler configuration.
type SchedulerConfig struct {
	Environment

	Postgres
	Scheduler
	Kafka
}

// Scheduler holds the configuration for the scheduler.
type Scheduler struct {
	PollInterval   time.Duration `envconfig:"SCHEDULER_POLL_INTERVAL" default:"10s"`
	ContextTimeout time.Duration `envconfig:"SCHEDULER_CONTEXT_TIMEOUT" default:"5s"`
	FetchLimit     int           `envconfig:"SCHEDULER_FETCH_LIMIT" default:"1000"`
	BatchSize      int           `envconfig:"SCHEDULER_BATCH_SIZE" default:"100"`
}

// InitSchedulingServiceConfig initializes the scheduler service configuration.
func InitSchedulingServiceConfig() (*SchedulerConfig, error) {
	var cfg SchedulerConfig
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
