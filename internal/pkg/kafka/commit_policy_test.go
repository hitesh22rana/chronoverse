package kafka_test

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
)

func TestShouldCommitOnError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "success",
			err:      nil,
			expected: true,
		},
		{
			name:     "internal error retryable",
			err:      status.Error(codes.Internal, "database unavilable"),
			expected: false,
		},
		{
			name:     "unavilable error retryable",
			err:      status.Error(codes.Unavailable, "service unavilable"),
			expected: false,
		},
		{
			name:     "deadline exceeded error retryable",
			err:      status.Error(codes.DeadlineExceeded, "operation timed out"),
			expected: false,
		},
		{
			name:     "aborted error retryable",
			err:      status.Error(codes.Aborted, "operation aborted"),
			expected: false,
		},
		{
			name:     "invalid argument error not retryable",
			err:      status.Error(codes.InvalidArgument, "invalid input"),
			expected: true,
		},
		{
			name:     "failed precondition error not retryable",
			err:      status.Error(codes.FailedPrecondition, "workflow already completed"),
			expected: true,
		},
		{
			name:     "not found error not retryable",
			err:      status.Error(codes.NotFound, "workflow not found"),
			expected: true,
		},
		{
			name:     "unknown non-status error not retryable",
			err:      status.Error(codes.Unknown, "unknown error"),
			expected: true,
		},
		{
			name:     "non-grpc error not retryable",
			err:      errors.New("some non-grpc error"),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := kafka.ShouldCommitOnError(tt.err); got != tt.expected {
				t.Fatalf("ShouldCommitOnError() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestShouldCommitOnErrorWithCustomRetryableCodes(t *testing.T) {
	t.Parallel()

	if got := kafka.ShouldCommitOnError(status.Error(codes.Aborted, "locks unavailable"), codes.Internal); got != true {
		t.Fatalf("ShouldCommitOnError() with custom retryable codes = %v, want true", got)
	}

	if got := kafka.ShouldCommitOnError(status.Error(codes.Internal, "database unavailable"), codes.Internal); got != false {
		t.Fatalf("ShouldCommitOnError() with custom retryable codes = %v, want false", got)
	}
}
