package config

import (
	"time"

	"github.com/kelseyhightower/envconfig"
)

// SchedulingWorker holds the scheduling worker configuration.
type SchedulingWorker struct {
	Environment

	Postgres
	Kafka
	SchedulingWorkerConfig
}

// SchedulingWorkerConfig holds the configuration for the scheduling worker.
type SchedulingWorkerConfig struct {
	PollInterval   time.Duration `envconfig:"SCHEDULING_WORKER_POLL_INTERVAL" default:"10s"`
	ContextTimeout time.Duration `envconfig:"SCHEDULING_WORKER_CONTEXT_TIMEOUT" default:"5s"`
	FetchLimit     int           `envconfig:"SCHEDULING_WORKER_FETCH_LIMIT" default:"1000"`
	BatchSize      int           `envconfig:"SCHEDULING_WORKER_BATCH_SIZE" default:"100"`
}

// InitSchedulingJobConfig initializes the scheduling worker configuration.
func InitSchedulingJobConfig() (*SchedulingWorker, error) {
	var cfg SchedulingWorker
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
