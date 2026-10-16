package databasemigration //nolint:testpackage // Exercises the private retry policy.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

func TestDatabaseErrorRetryClassification(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"reset", errors.New("read: connection reset by peer"), true},
		{"wrapped network", fmt.Errorf("migrate: %w", errors.New("NETWORK IS UNREACHABLE")), true},
		{"TLS timeout", errors.New("TLS handshake timeout"), true},
		{"broken pipe", errors.New("write: broken pipe"), true},
		{"certificate", errors.New("x509: certificate has expired"), false},
		{"authentication", errors.New("AUTHENTICATION failed"), false},
		{"syntax", errors.New("database: syntax error at SELECT"), false},
		{"constraint", errors.New("violates unique constraint"), false},
		{"unknown", errors.New("unclassified storage failure"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := (&Repository{}).isDatabaseErrorRetryable(tc.err); got != tc.want {
				t.Fatalf("retryable(%v) = %t; want %t", tc.err, got, tc.want)
			}
		})
	}
}

func TestWithRetryBackoffAndOutcomes(t *testing.T) {
	t.Parallel()
	transient := errors.New("connection reset by peer")
	last := errors.New("connection refused on last attempt")
	permanent := errors.New("permission denied")
	for _, tc := range []struct {
		name    string
		results []error
		wantErr error
		offsets []time.Duration
	}{
		{"first success", []error{nil}, nil, []time.Duration{0}},
		{"retry success", []error{transient, transient, nil}, nil, []time.Duration{0, time.Second, 3 * time.Second}},
		{"exhausted", []error{transient, last}, last, []time.Duration{0, time.Second, 3 * time.Second, 5 * time.Second}},
		{"permanent", []error{permanent}, permanent, []time.Duration{0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				var offsets []time.Duration
				operation := func() error {
					index := min(len(offsets), len(tc.results)-1)
					offsets = append(offsets, time.Since(start))
					return tc.results[index]
				}
				cfg := RetryConfig{MaxRetries: 4, InitialDelay: time.Second, MaxDelay: 2 * time.Second, BackoffFactor: 3}
				err := (&Repository{}).withRetry(t.Context(), "postgres", cfg, operation)
				if !errors.Is(err, tc.wantErr) || !slices.Equal(offsets, tc.offsets) {
					t.Fatalf("retry error=%v offsets=%v; want %v, %v", err, offsets, tc.wantErr, tc.offsets)
				}
			})
		})
	}
}

func TestWithRetryInterruptsBackoff(t *testing.T) {
	t.Parallel()
	for _, deadline := range []bool{false, true} {
		name := "cancel"
		if deadline {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				const interruptAfter = 250 * time.Millisecond
				ctx, cancel := context.WithCancel(t.Context())
				want := context.Canceled
				if deadline {
					cancel()
					ctx, cancel = context.WithTimeout(t.Context(), interruptAfter)
					want = context.DeadlineExceeded
				} else {
					go func() {
						time.Sleep(interruptAfter)
						cancel()
					}()
				}
				defer cancel()
				start, calls := time.Now(), 0
				cfg := RetryConfig{MaxRetries: 4, InitialDelay: time.Second, MaxDelay: 2 * time.Second, BackoffFactor: 2}
				err := (&Repository{}).withRetry(ctx, "postgres", cfg, func() error {
					calls++
					return errors.New("connection refused")
				})
				if !errors.Is(err, want) || calls != 1 || time.Since(start) != interruptAfter {
					t.Fatalf("retry error=%v calls=%d elapsed=%v; want %v, 1, %v", err, calls, time.Since(start), want, interruptAfter)
				}
			})
		})
	}
}
