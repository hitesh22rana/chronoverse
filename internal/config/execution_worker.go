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
	Redis
	WorkflowsService
	JobsService
	NotificationsService
	ExecutionWorkerConfig
}

// ExecutionWorkerConfig holds the configuration for the execution worker.
type ExecutionWorkerConfig struct {
	WorkerID                    string        `envconfig:"EXECUTION_WORKER_ID" default:""`
	Concurrency                 int           `envconfig:"EXECUTION_WORKER_CONCURRENCY" default:"0"`
	AwaitingReconciliationLimit int           `envconfig:"EXECUTION_WORKER_AWAITING_RECONCILIATION_LIMIT" default:"0"`
	LeaseDuration               time.Duration `envconfig:"EXECUTION_WORKER_LEASE_DURATION" default:"30s"`
	LeaseRenewInterval          time.Duration `envconfig:"EXECUTION_WORKER_LEASE_RENEW_INTERVAL" default:"10s"`
	SystemRetryLimit            int           `envconfig:"EXECUTION_WORKER_SYSTEM_RETRY_LIMIT" default:"3"`
	SystemRetryBackoff          time.Duration `envconfig:"EXECUTION_WORKER_SYSTEM_RETRY_BACKOFF" default:"30s"`
	RecoveryInterval            time.Duration `envconfig:"EXECUTION_WORKER_RECOVERY_INTERVAL" default:"15s"`
	RecoveryBatchSize           int32         `envconfig:"EXECUTION_WORKER_RECOVERY_BATCH_SIZE" default:"100"`
	JobLogBatchSize             int           `envconfig:"EXECUTION_WORKER_JOB_LOG_BATCH_SIZE" default:"100"`
	JobLogBatchInterval         time.Duration `envconfig:"EXECUTION_WORKER_JOB_LOG_BATCH_INTERVAL" default:"250ms"`
	JobLogPublishTimeout        time.Duration `envconfig:"EXECUTION_WORKER_JOB_LOG_PUBLISH_TIMEOUT" default:"5s"`
	JobLogPublishRetries        int           `envconfig:"EXECUTION_WORKER_JOB_LOG_PUBLISH_RETRIES" default:"3"`
	JobLogPublishBackoff        time.Duration `envconfig:"EXECUTION_WORKER_JOB_LOG_PUBLISH_BACKOFF" default:"250ms"`
	JobLogLiveTimeout           time.Duration `envconfig:"EXECUTION_WORKER_JOB_LOG_LIVE_TIMEOUT" default:"100ms"`
	JobLogLiveBufferSize        int           `envconfig:"EXECUTION_WORKER_JOB_LOG_LIVE_BUFFER_SIZE" default:"4096"`
	ImagePullLockTTL            time.Duration `envconfig:"EXECUTION_WORKER_IMAGE_PULL_LOCK_TTL" default:"10m"`
	ImagePullLockWaitTimeout    time.Duration `envconfig:"EXECUTION_WORKER_IMAGE_PULL_LOCK_WAIT_TIMEOUT" default:"10m"`
	ImagePullLockRetryInterval  time.Duration `envconfig:"EXECUTION_WORKER_IMAGE_PULL_LOCK_RETRY_INTERVAL" default:"500ms"`
	WorkloadMemory              string        `envconfig:"EXECUTION_WORKER_WORKLOAD_CONTAINER_MEMORY" default:"512m"`
	WorkloadCPUs                float64       `envconfig:"EXECUTION_WORKER_WORKLOAD_CONTAINER_CPUS" default:"1"`
	WorkloadPidsLimit           int64         `envconfig:"EXECUTION_WORKER_WORKLOAD_CONTAINER_PIDS_LIMIT" default:"256"`
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
