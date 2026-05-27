package config

import (
	"os"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// ExecutionWorker holds the execution worker configuration.
type ExecutionWorker struct {
	Environment

	ClientTLS
	Kafka
	WorkflowsService
	JobsService
	NotificationsService
	ExecutionWorkerConfig
}

// ExecutionWorkerConfig holds the configuration for the execution worker.
type ExecutionWorkerConfig struct {
	WorkerID           string        `envconfig:"EXECUTION_WORKER_ID" default:""`
	Concurrency        int           `envconfig:"EXECUTION_WORKER_CONCURRENCY" default:"0"`
	LeaseDuration      time.Duration `envconfig:"EXECUTION_WORKER_LEASE_DURATION" default:"30s"`
	LeaseRenewInterval time.Duration `envconfig:"EXECUTION_WORKER_LEASE_RENEW_INTERVAL" default:"10s"`
	SystemRetryLimit   int           `envconfig:"EXECUTION_WORKER_SYSTEM_RETRY_LIMIT" default:"3"`
	SystemRetryBackoff time.Duration `envconfig:"EXECUTION_WORKER_SYSTEM_RETRY_BACKOFF" default:"30s"`
	RecoveryInterval   time.Duration `envconfig:"EXECUTION_WORKER_RECOVERY_INTERVAL" default:"15s"`
	RecoveryBatchSize  int32         `envconfig:"EXECUTION_WORKER_RECOVERY_BATCH_SIZE" default:"100"`
}

// InitExecutionJobConfig initializes the execution worker configuration.
func InitExecutionJobConfig() (*ExecutionWorker, error) {
	var cfg ExecutionWorker
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	if cfg.ExecutionWorkerConfig.WorkerID == "" {
		if hostname, err := os.Hostname(); err == nil && hostname != "" {
			cfg.ExecutionWorkerConfig.WorkerID = hostname
		} else {
			cfg.ExecutionWorkerConfig.WorkerID = "execution-worker"
		}
	}
	return &cfg, nil
}
