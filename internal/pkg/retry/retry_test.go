package retry_test

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/retry"
)

func TestOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		err           error
		expectedCalls int
	}{
		{
			name:          "success on first attempt",
			err:           nil,
			expectedCalls: 1,
		},
		{
			name:          "internal retries by default",
			err:           status.Error(codes.Internal, "database unavailable"),
			expectedCalls: 2,
		},
		{
			name:          "unavailable retries by default",
			err:           status.Error(codes.Unavailable, "service unavailable"),
			expectedCalls: 2,
		},
		{
			name:          "non-status error retries by default",
			err:           errors.New("plain error"),
			expectedCalls: 2,
		},
		{
			name:          "invalid argument does not retry by default",
			err:           status.Error(codes.InvalidArgument, "invalid argument"),
			expectedCalls: 1,
		},
		{
			name:          "failed precondition does not retry by default",
			err:           status.Error(codes.FailedPrecondition, "failed precondition"),
			expectedCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			err := retry.Once(func() error {
				calls++
				return tt.err
			}, 0)

			if !sameErrorCode(err, tt.err) {
				t.Fatalf("Once() error = %v, want %v", err, tt.err)
			}
			if calls != tt.expectedCalls {
				t.Fatalf("Once() calls = %d, want %d", calls, tt.expectedCalls)
			}
		})
	}
}

func TestOnceWithCustomRetryableCodes(t *testing.T) {
	t.Parallel()

	calls := 0
	err := retry.Once(func() error {
		calls++
		return status.Error(codes.Aborted, "invalid argument")
	}, 0, codes.Internal)
	if status.Code(err) != codes.Aborted {
		t.Fatalf("Once() error = %v, want code %v", err, codes.Aborted)
	}
	if calls != 1 {
		t.Fatalf("Once() calls = %d, want %d", calls, 1)
	}

	calls = 0
	err = retry.Once(func() error {
		calls++
		return status.Error(codes.Internal, "invalid argument")
	}, 0, codes.Internal)
	if status.Code(err) != codes.Internal {
		t.Fatalf("Once() error = %v, want code %v", err, codes.Internal)
	}
	if calls != 2 {
		t.Fatalf("Once() calls = %d, want %d", calls, 2)
	}
}

func TestOnceBackoff(t *testing.T) {
	t.Parallel()

	start := time.Now()
	//nolint:errcheck // We are only interested in the backoff behavior, not the error
	_ = retry.Once(func() error {
		return status.Error(codes.Internal, "database unavailable")
	}, time.Millisecond)

	if time.Since(start) < time.Millisecond {
		t.Fatal("Once() did not wait for backoff duration")
	}
}

func sameErrorCode(got, want error) bool {
	if got == nil || want == nil {
		return errors.Is(got, want)
	}

	return status.Code(got) == status.Code(want)
}
