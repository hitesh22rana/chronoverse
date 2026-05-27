package executor

import (
	"runtime"
	"time"
)

func normalizeConfig(cfg *Config) Config {
	normalized := Config{
		WorkerID:           "execution-worker",
		Concurrency:        runtime.GOMAXPROCS(0),
		LeaseDuration:      30 * time.Second,
		LeaseRenewInterval: 10 * time.Second,
		SystemRetryLimit:   3,
		SystemRetryBackoff: 30 * time.Second,
		RecoveryInterval:   15 * time.Second,
		RecoveryBatchSize:  100,
	}
	if cfg == nil {
		if normalized.Concurrency <= 0 {
			normalized.Concurrency = 1
		}
		return normalized
	}
	if cfg.WorkerID != "" {
		normalized.WorkerID = cfg.WorkerID
	}
	if cfg.Concurrency > 0 {
		normalized.Concurrency = cfg.Concurrency
	}
	if normalized.Concurrency <= 0 {
		normalized.Concurrency = 1
	}
	if cfg.LeaseDuration > 0 {
		normalized.LeaseDuration = cfg.LeaseDuration
	}
	if cfg.LeaseRenewInterval > 0 {
		normalized.LeaseRenewInterval = cfg.LeaseRenewInterval
	}
	if normalized.LeaseRenewInterval >= normalized.LeaseDuration {
		normalized.LeaseRenewInterval = normalized.LeaseDuration / 3
	}
	if normalized.LeaseRenewInterval <= 0 {
		normalized.LeaseRenewInterval = time.Second
	}
	if cfg.SystemRetryLimit > 0 {
		normalized.SystemRetryLimit = cfg.SystemRetryLimit
	}
	if cfg.SystemRetryBackoff > 0 {
		normalized.SystemRetryBackoff = cfg.SystemRetryBackoff
	}
	if cfg.RecoveryInterval > 0 {
		normalized.RecoveryInterval = cfg.RecoveryInterval
	}
	if cfg.RecoveryBatchSize > 0 {
		normalized.RecoveryBatchSize = cfg.RecoveryBatchSize
	}

	return normalized
}
