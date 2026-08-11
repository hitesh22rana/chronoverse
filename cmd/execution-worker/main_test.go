package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/retry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestExecutionWorkerRetryConfig(t *testing.T) {
	t.Parallel()

	cfg := executionWorkerRetryConfig()
	if cfg.MaxAttempts != 3 {
		t.Fatalf("MaxAttempts = %d, want 3", cfg.MaxAttempts)
	}
	if cfg.BackoffExponential != 100*time.Millisecond {
		t.Fatalf("BackoffExponential = %s, want 100ms", cfg.BackoffExponential)
	}
	if !reflect.DeepEqual(cfg.RetryableCodes, []codes.Code{codes.Unavailable, codes.DeadlineExceeded}) {
		t.Fatalf("RetryableCodes = %v, want Unavailable and DeadlineExceeded", cfg.RetryableCodes)
	}
}

func TestExecutionWorkerRetryConfigPerformsThreeAttempts(t *testing.T) {
	t.Parallel()

	cfg := executionWorkerRetryConfig()
	interceptor := retry.UnaryClientInterceptor(
		retry.WithCodes(cfg.RetryableCodes...),
		retry.WithMax(cfg.MaxAttempts),
		retry.WithBackoff(retry.BackoffExponential(cfg.BackoffExponential)),
		retry.WithPerRetryTimeout(cfg.PerRetryTimeout),
	)

	attempts := make([]time.Time, 0, cfg.MaxAttempts)
	err := interceptor(
		context.Background(),
		"/test.Service/Command",
		nil,
		nil,
		nil,
		func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
			attempts = append(attempts, time.Now())
			if len(attempts) < int(cfg.MaxAttempts) {
				return status.Error(codes.Unavailable, "retry")
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("retry interceptor error = %v", err)
	}
	if len(attempts) != 3 {
		t.Fatalf("attempts = %d, want 3", len(attempts))
	}
	if delay := attempts[1].Sub(attempts[0]); delay < 100*time.Millisecond {
		t.Fatalf("first retry delay = %s, want at least 100ms", delay)
	}
	if delay := attempts[2].Sub(attempts[1]); delay < 200*time.Millisecond {
		t.Fatalf("second retry delay = %s, want at least 200ms", delay)
	}
}
