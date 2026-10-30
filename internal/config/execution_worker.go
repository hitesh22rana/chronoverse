package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/docker/go-units"
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
	DockerProxy
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
	HeartbeatNAT64Prefixes      []string      `envconfig:"EXECUTION_WORKER_HEARTBEAT_NAT64_PREFIXES" default:""`
	ImagePullLockTTL            time.Duration `envconfig:"EXECUTION_WORKER_IMAGE_PULL_LOCK_TTL" default:"10m"`
	ImagePullLockWaitTimeout    time.Duration `envconfig:"EXECUTION_WORKER_IMAGE_PULL_LOCK_WAIT_TIMEOUT" default:"10m"`
	ImagePullLockRetryInterval  time.Duration `envconfig:"EXECUTION_WORKER_IMAGE_PULL_LOCK_RETRY_INTERVAL" default:"500ms"`
	WorkloadMemory              string        `envconfig:"EXECUTION_WORKER_WORKLOAD_CONTAINER_MEMORY" default:"512m"`
	WorkloadCPUs                float64       `envconfig:"EXECUTION_WORKER_WORKLOAD_CONTAINER_CPUS" default:"1"`
	WorkloadPidsLimit           int64         `envconfig:"EXECUTION_WORKER_WORKLOAD_CONTAINER_PIDS_LIMIT" default:"256"`
	WorkloadNetwork             string        `envconfig:"EXECUTION_WORKER_WORKLOAD_NETWORK" default:"chronoverse-workloads"`
	WorkloadSubnet              string        `envconfig:"EXECUTION_WORKER_WORKLOAD_SUBNET" default:"198.18.247.0/24"`
}

// InitExecutionJobConfig initializes the execution worker configuration.
func InitExecutionJobConfig() (*ExecutionWorker, error) {
	var cfg ExecutionWorker
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	if err := cfg.ExecutionWorkerConfig.validate(); err != nil {
		return nil, err
	}
	if cfg.ExecutionWorkerConfig.WorkerID == "" {
		cfg.ExecutionWorkerConfig.WorkerID = "execution-worker"
		if hostname, err := os.Hostname(); err == nil && hostname != "" {
			cfg.ExecutionWorkerConfig.WorkerID = hostname
		}
	}
	return &cfg, nil
}

// validate rejects workload container limits that the Docker runtime would
// silently ignore or misinterpret.
func (c *ExecutionWorkerConfig) validate() error {
	memoryBytes, err := units.RAMInBytes(c.WorkloadMemory)
	if err != nil {
		return fmt.Errorf("invalid EXECUTION_WORKER_WORKLOAD_CONTAINER_MEMORY: %w", err)
	}
	// The RAM parser's overflowing float-to-int conversion can wrap or saturate.
	if memoryBytes < 0 || memoryBytes == math.MaxInt64 {
		return errors.New("invalid EXECUTION_WORKER_WORKLOAD_CONTAINER_MEMORY: exceeds supported byte range")
	}
	if math.IsNaN(c.WorkloadCPUs) || math.IsInf(c.WorkloadCPUs, 0) {
		return errors.New("invalid EXECUTION_WORKER_WORKLOAD_CONTAINER_CPUS: must be finite")
	}
	if c.WorkloadCPUs < 0 {
		return errors.New("invalid EXECUTION_WORKER_WORKLOAD_CONTAINER_CPUS: must be non-negative")
	}
	if c.WorkloadPidsLimit < 0 {
		return errors.New("invalid EXECUTION_WORKER_WORKLOAD_CONTAINER_PIDS_LIMIT: must be non-negative")
	}

	nanoCPUs := math.Round(c.WorkloadCPUs * 1e9)
	// float64(math.MaxInt64) rounds up to 2^63, so values below this boundary
	// always convert to int64 without overflowing.
	if nanoCPUs >= float64(math.MaxInt64) {
		return errors.New("invalid EXECUTION_WORKER_WORKLOAD_CONTAINER_CPUS: exceeds supported nanocpu range")
	}
	if c.WorkloadCPUs > 0 && nanoCPUs == 0 {
		return errors.New("invalid EXECUTION_WORKER_WORKLOAD_CONTAINER_CPUS: positive limits must round to at least one nanocpu")
	}
	return nil
}
