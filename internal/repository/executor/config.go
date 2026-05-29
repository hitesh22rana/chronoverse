package executor

import (
	"runtime"
	"time"
)

func normalizeConfig(cfg *Config) Config {
	normalized := defaultConfig()
	if cfg != nil {
		applyConfigOverrides(&normalized, cfg)
	}
	normalizeConcurrency(&normalized)
	normalizeLeaseIntervals(&normalized)

	return normalized
}

func defaultConfig() Config {
	return Config{
		WorkerID:             "execution-worker",
		Concurrency:          runtime.GOMAXPROCS(0),
		LeaseDuration:        30 * time.Second,
		LeaseRenewInterval:   10 * time.Second,
		SystemRetryLimit:     3,
		SystemRetryBackoff:   30 * time.Second,
		RecoveryInterval:     15 * time.Second,
		RecoveryBatchSize:    100,
		JobLogBatchSize:      100,
		JobLogBatchInterval:  250 * time.Millisecond,
		JobLogPublishTimeout: 5 * time.Second,
		JobLogPublishRetries: 3,
		JobLogPublishBackoff: 250 * time.Millisecond,
		JobLogLiveTimeout:    100 * time.Millisecond,
		JobLogLiveBufferSize: 4096,
	}
}

func applyConfigOverrides(normalized, cfg *Config) {
	applyWorkerConfigOverrides(normalized, cfg)
	applySystemRetryConfigOverrides(normalized, cfg)
	applyJobLogConfigOverrides(normalized, cfg)
}

func applyWorkerConfigOverrides(normalized, cfg *Config) {
	if cfg.WorkerID != "" {
		normalized.WorkerID = cfg.WorkerID
	}
	if cfg.Concurrency > 0 {
		normalized.Concurrency = cfg.Concurrency
	}
	if cfg.LeaseDuration > 0 {
		normalized.LeaseDuration = cfg.LeaseDuration
	}
	if cfg.LeaseRenewInterval > 0 {
		normalized.LeaseRenewInterval = cfg.LeaseRenewInterval
	}
	if cfg.RecoveryInterval > 0 {
		normalized.RecoveryInterval = cfg.RecoveryInterval
	}
	if cfg.RecoveryBatchSize > 0 {
		normalized.RecoveryBatchSize = cfg.RecoveryBatchSize
	}
}

func applySystemRetryConfigOverrides(normalized, cfg *Config) {
	if cfg.SystemRetryLimit > 0 {
		normalized.SystemRetryLimit = cfg.SystemRetryLimit
	}
	if cfg.SystemRetryBackoff > 0 {
		normalized.SystemRetryBackoff = cfg.SystemRetryBackoff
	}
}

func applyJobLogConfigOverrides(normalized, cfg *Config) {
	if cfg.JobLogBatchSize > 0 {
		normalized.JobLogBatchSize = cfg.JobLogBatchSize
	}
	if cfg.JobLogBatchInterval > 0 {
		normalized.JobLogBatchInterval = cfg.JobLogBatchInterval
	}
	if cfg.JobLogPublishTimeout > 0 {
		normalized.JobLogPublishTimeout = cfg.JobLogPublishTimeout
	}
	if cfg.JobLogPublishRetries > 0 {
		normalized.JobLogPublishRetries = cfg.JobLogPublishRetries
	}
	if cfg.JobLogPublishBackoff > 0 {
		normalized.JobLogPublishBackoff = cfg.JobLogPublishBackoff
	}
	if cfg.JobLogLiveTimeout > 0 {
		normalized.JobLogLiveTimeout = cfg.JobLogLiveTimeout
	}
	if cfg.JobLogLiveBufferSize > 0 {
		normalized.JobLogLiveBufferSize = cfg.JobLogLiveBufferSize
	}
}

func normalizeConcurrency(cfg *Config) {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}
}

func normalizeLeaseIntervals(cfg *Config) {
	if cfg.LeaseRenewInterval >= cfg.LeaseDuration {
		cfg.LeaseRenewInterval = cfg.LeaseDuration / 3
	}
	if cfg.LeaseRenewInterval <= 0 {
		cfg.LeaseRenewInterval = time.Second
	}
}
