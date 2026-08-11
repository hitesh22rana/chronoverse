package main

import (
	"reflect"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
)

func TestExecutionWorkerRetryConfig(t *testing.T) {
	t.Parallel()

	cfg := executionWorkerRetryConfig()
	if cfg.MaxRetries != 2 {
		t.Fatalf("MaxRetries = %d, want 2", cfg.MaxRetries)
	}
	if cfg.BackoffExponential != 100*time.Millisecond {
		t.Fatalf("BackoffExponential = %s, want 100ms", cfg.BackoffExponential)
	}
	if !reflect.DeepEqual(cfg.RetryableCodes, []codes.Code{codes.Unavailable, codes.DeadlineExceeded}) {
		t.Fatalf("RetryableCodes = %v, want Unavailable and DeadlineExceeded", cfg.RetryableCodes)
	}
}
