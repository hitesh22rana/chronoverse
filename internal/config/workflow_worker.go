package config

import (
	"time"

	"github.com/kelseyhightower/envconfig"
)

// WorkflowWorker holds the workflow worker configuration.
type WorkflowWorker struct {
	Environment

	Redis
	ClickHouse
	MeiliSearch
	ClientTLS
	Kafka
	WorkflowsService
	JobsService
	NotificationsService
	WorkflowWorkerConfig
}

// WorkflowWorkerConfig holds the configuration for the workflow worker.
type WorkflowWorkerConfig struct {
	ImagePullLockTTL           time.Duration `envconfig:"WORKFLOW_WORKER_IMAGE_PULL_LOCK_TTL" default:"10m"`
	ImagePullLockWaitTimeout   time.Duration `envconfig:"WORKFLOW_WORKER_IMAGE_PULL_LOCK_WAIT_TIMEOUT" default:"10m"`
	ImagePullLockRetryInterval time.Duration `envconfig:"WORKFLOW_WORKER_IMAGE_PULL_LOCK_RETRY_INTERVAL" default:"500ms"`
}

// InitWorkflowWorkerConfig initializes the workflow worker configuration.
func InitWorkflowWorkerConfig() (*WorkflowWorker, error) {
	var cfg WorkflowWorker
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
