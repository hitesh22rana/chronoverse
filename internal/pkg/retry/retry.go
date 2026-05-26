package retry

import (
	"slices"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var defaultNonRetryableErrorCodes = []codes.Code{
	codes.FailedPrecondition,
	codes.InvalidArgument,
}

// Once executes the fn and retries it once after backoff if the error is retryable.
// With no retryableCodes, every code except the package default non-retryable set is retried.
func Once(fn func() error, backoff time.Duration, retryableCodes ...codes.Code) error {
	err := fn()
	if err == nil {
		return nil
	}

	if len(retryableCodes) == 0 {
		if slices.Contains(defaultNonRetryableErrorCodes, status.Code(err)) {
			return err
		}
	} else if !slices.Contains(retryableCodes, status.Code(err)) {
		return err
	}

	time.Sleep(backoff)
	return fn()
}
