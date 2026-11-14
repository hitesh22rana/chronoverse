//nolint:testpackage // Exercises the shared lease policy through Ensure.
package imagepull

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestEnsureRejectsInvalidTTLBeforeExternalCalls(t *testing.T) {
	for _, ttl := range []time.Duration{-time.Second, time.Nanosecond, time.Millisecond, 3*time.Millisecond - time.Nanosecond} {
		if err := Ensure(t.Context(), nil, nil, "image", Config{TTL: ttl}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("TTL %s: %v", ttl, err)
		}
	}
}

func TestEnsureNormalizesLeaseToRedisPrecision(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := contractClient{inspect: func(context.Context, string) (bool, error) { return false, nil }, build: func(context.Context, string) error { time.Sleep(10 * time.Millisecond); return nil }}
		renewals := 0
		locks := contractLocks{
			acquire: func(_ context.Context, _ string, ttl time.Duration) (string, bool, error) {
				if ttl != 3*time.Millisecond {
					t.Errorf("Redis acquisition TTL = %s", ttl)
				}
				return "owner", true, nil
			},
			extend: func(_ context.Context, _, _ string, ttl time.Duration) (bool, error) {
				renewals++
				if ttl != 3*time.Millisecond {
					t.Errorf("Redis renewal TTL = %s", ttl)
				}
				return true, nil
			},
			release: func(context.Context, string, string) error { return nil },
		}
		if err := Ensure(t.Context(), client, locks, "image", Config{TTL: 3900 * time.Microsecond}); err != nil || renewals == 0 {
			t.Fatalf("millisecond-precision lease = %v; renewals=%d", err, renewals)
		}
	})
}

func TestEnsureCapsRenewalByRemainingLeaseAfterSlowAcquisition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		client := contractClient{inspect: func(context.Context, string) (bool, error) { return false, nil }, build: func(ctx context.Context, _ string) error { <-ctx.Done(); return ctx.Err() }}
		locks := contractLocks{
			acquire: func(context.Context, string, time.Duration) (string, bool, error) {
				time.Sleep(1500 * time.Millisecond)
				return "owner", true, nil
			},
			extend: func(ctx context.Context, _, _ string, _ time.Duration) (bool, error) {
				deadline, ok := ctx.Deadline()
				if !ok || !deadline.Equal(start.Add(2*time.Second)) {
					t.Errorf("remaining-lease deadline = %v", deadline)
				}
				<-ctx.Done()
				return false, ctx.Err()
			},
			release: func(context.Context, string, string) error { return nil },
		}
		err := Ensure(t.Context(), client, locks, "image", Config{TTL: 3 * time.Second})
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != 2*time.Second {
			t.Fatalf("delayed lease result = %v after %s", err, time.Since(start))
		}
	})
}

func TestEnsureRejectsExhaustedAcquisitionBeforeReinspection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inspections, releases := 0, 0
		client := contractClient{inspect: func(context.Context, string) (bool, error) { inspections++; return false, nil }}
		locks := contractLocks{
			acquire: func(context.Context, string, time.Duration) (string, bool, error) {
				time.Sleep(2 * time.Second)
				return "owner", true, nil
			},
			release: func(context.Context, string, string) error { releases++; return nil },
		}
		err := Ensure(t.Context(), client, locks, "image", Config{TTL: 3 * time.Second})
		if status.Code(err) != codes.Unavailable || inspections != 1 || releases != 1 {
			t.Fatalf("expired acquisition = %v; inspections=%d releases=%d", err, inspections, releases)
		}
	})
}

func TestEnsureRenewsDuringReinspectionAndStopsBeforeBuild(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inspections, builds := 0, 0
		client := contractClient{inspect: func(ctx context.Context, _ string) (bool, error) {
			inspections++
			if inspections == 2 {
				<-ctx.Done()
			}
			return false, nil // Successful response racing with ownership loss.
		}, build: func(context.Context, string) error { builds++; return nil }}
		locks := contractLocks{
			acquire: func(context.Context, string, time.Duration) (string, bool, error) { return "owner", true, nil },
			extend:  func(context.Context, string, string, time.Duration) (bool, error) { return false, nil },
			release: func(context.Context, string, string) error { return nil },
		}
		err := Ensure(t.Context(), client, locks, "image", Config{TTL: 3 * time.Second})
		if status.Code(err) != codes.ResourceExhausted || builds != 0 || inspections != 2 {
			t.Fatalf("ownership-lost inspection = %v; inspections=%d builds=%d", err, inspections, builds)
		}
	})
}

func TestEnsurePreservesIndependentWorkErrorDuringRenewalFailure(t *testing.T) {
	renewErr := errors.New("Redis unavailable")
	workErr := errors.New("registry denied image")
	for _, failure := range []error{
		nil, context.Canceled, fmt.Errorf("build: %w", context.DeadlineExceeded),
		status.Error(codes.Canceled, "canceled"), status.Error(codes.DeadlineExceeded, "expired"), workErr,
	} {
		t.Run(fmt.Sprintf("%v", failure), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client := contractClient{inspect: func(context.Context, string) (bool, error) { return false, nil }, build: func(ctx context.Context, _ string) error { <-ctx.Done(); return failure }}
				locks := contractLocks{
					acquire: func(context.Context, string, time.Duration) (string, bool, error) { return "owner", true, nil },
					extend:  func(context.Context, string, string, time.Duration) (bool, error) { return false, renewErr },
					release: func(context.Context, string, string) error { return nil },
				}
				err := Ensure(t.Context(), client, locks, "image", Config{TTL: 3 * time.Second})
				want := renewErr
				if errors.Is(failure, workErr) {
					want = workErr
				}
				if !errors.Is(err, want) {
					t.Fatalf("work/renewal result = %v, want %v", err, want)
				}
			})
		})
	}
}

func TestEnsureExhaustsLeaseAfterDelayedRenewalResponse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := contractClient{inspect: func(context.Context, string) (bool, error) { return false, nil }, build: func(ctx context.Context, _ string) error { <-ctx.Done(); return ctx.Err() }}
		calls := 0
		locks := contractLocks{
			acquire: func(context.Context, string, time.Duration) (string, bool, error) { return "owner", true, nil },
			extend: func(context.Context, string, string, time.Duration) (bool, error) {
				calls++
				time.Sleep(4 * time.Second) // A successful response observed after a worker scheduling stall.
				return true, nil
			},
			release: func(context.Context, string, string) error { return nil },
		}
		err := Ensure(t.Context(), client, locks, "image", Config{TTL: 3 * time.Second})
		if status.Code(err) != codes.Unavailable || calls != 1 {
			t.Fatalf("exhausted renewal = %v; calls=%d", err, calls)
		}
	})
}
