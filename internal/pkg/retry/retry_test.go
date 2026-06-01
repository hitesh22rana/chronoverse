package retry_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/retry"
)

func TestDoDefaultRetryPolicy(t *testing.T) {
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
			err := retry.Do(t.Context(), 2, 0, func() error {
				calls++
				return tt.err
			})

			if !sameErrorCode(err, tt.err) {
				t.Fatalf("Do() error = %v, want %v", err, tt.err)
			}
			if calls != tt.expectedCalls {
				t.Fatalf("Do() calls = %d, want %d", calls, tt.expectedCalls)
			}
		})
	}
}

func TestDoWithCustomRetryableCodes(t *testing.T) {
	t.Parallel()

	calls := 0
	err := retry.Do(t.Context(), 2, 0, func() error {
		calls++
		return status.Error(codes.Aborted, "invalid argument")
	}, codes.Internal)
	if status.Code(err) != codes.Aborted {
		t.Fatalf("Do() error = %v, want code %v", err, codes.Aborted)
	}
	if calls != 1 {
		t.Fatalf("Do() calls = %d, want %d", calls, 1)
	}

	calls = 0
	err = retry.Do(t.Context(), 2, 0, func() error {
		calls++
		return status.Error(codes.Internal, "invalid argument")
	}, codes.Internal)
	if status.Code(err) != codes.Internal {
		t.Fatalf("Do() error = %v, want code %v", err, codes.Internal)
	}
	if calls != 2 {
		t.Fatalf("Do() calls = %d, want %d", calls, 2)
	}
}

func TestDoBackoff(t *testing.T) {
	t.Parallel()

	start := time.Now()
	//nolint:errcheck // We are only interested in the backoff behavior, not the error
	_ = retry.Do(t.Context(), 2, time.Millisecond, func() error {
		return status.Error(codes.Internal, "database unavailable")
	})

	if time.Since(start) < time.Millisecond {
		t.Fatal("Do() did not wait for backoff duration")
	}
}

func TestDoRetriesConfiguredAttempts(t *testing.T) {
	t.Parallel()

	calls := 0
	err := retry.Do(t.Context(), 3, 0, func() error {
		calls++
		if calls < 3 {
			return status.Error(codes.Unavailable, "service unavailable")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do() error = %v, want nil", err)
	}
	if calls != 3 {
		t.Fatalf("Do() calls = %d, want 3", calls)
	}
}

func TestDoStopsOnNonRetryableError(t *testing.T) {
	t.Parallel()

	calls := 0
	err := retry.Do(t.Context(), 3, 0, func() error {
		calls++
		return status.Error(codes.InvalidArgument, "invalid")
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("Do() error = %v, want code %v", err, codes.InvalidArgument)
	}
	if calls != 1 {
		t.Fatalf("Do() calls = %d, want 1", calls)
	}
}

func TestDoHonorsContextDuringBackoff(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	err := retry.Do(ctx, 3, time.Hour, func() error {
		calls++
		cancel()
		return status.Error(codes.Unavailable, "service unavailable")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Do() error = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Fatalf("Do() calls = %d, want 1", calls)
	}
}

func sameErrorCode(got, want error) bool {
	if got == nil || want == nil {
		return errors.Is(got, want)
	}

	return status.Code(got) == status.Code(want)
}
