package retry

import (
	"context"
	"slices"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var defaultNonRetryableErrorCodes = []codes.Code{
	codes.FailedPrecondition,
	codes.InvalidArgument,
}

// Do executes fn up to attempts times, waiting backoff between retryable failures.
// With no retryableCodes, every code except the package default non-retryable set is retried.
func Do(ctx context.Context, attempts int, backoff time.Duration, fn func() error, retryableCodes ...codes.Code) error {
	if attempts <= 0 {
		attempts = 1
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		err := fn()
		if err == nil {
			return nil
		}
		if attempt == attempts || !isRetryable(err, retryableCodes...) {
			return err
		}
		if !sleepWithContext(ctx, backoff) {
			return ctx.Err()
		}
	}

	return nil
}

func isRetryable(err error, retryableCodes ...codes.Code) bool {
	if len(retryableCodes) == 0 {
		return !slices.Contains(defaultNonRetryableErrorCodes, status.Code(err))
	}

	return slices.Contains(retryableCodes, status.Code(err))
}

func sleepWithContext(ctx context.Context, duration time.Duration) bool {
	if duration <= 0 {
		return true
	}

	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
