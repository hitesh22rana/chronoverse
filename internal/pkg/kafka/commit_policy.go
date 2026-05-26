package kafka

import (
	"slices"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var defaultRetryableCommitErrorCodes = []codes.Code{
	codes.Aborted,
	codes.Canceled,
	codes.DeadlineExceeded,
	codes.Internal,
	codes.ResourceExhausted,
	codes.Unavailable,
}

// ShouldCommitOnError reports whether a Kafka record should be committed after err,
// With no retryableCodes, the package default retryable set is used.
func ShouldCommitOnError(err error, retryableCodes ...codes.Code) bool {
	if err == nil {
		return true
	}

	if len(retryableCodes) == 0 {
		retryableCodes = defaultRetryableCommitErrorCodes
	}

	return !slices.Contains(retryableCodes, status.Code(err))
}
