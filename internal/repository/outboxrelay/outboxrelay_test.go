//nolint:testpackage // Tests unexported query helper behavior without widening production API.
package outboxrelay

import (
	"encoding/hex"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPostgresInterval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{
			name:     "seconds",
			duration: 30 * time.Second,
			expected: "30000 milliseconds",
		},
		{
			name:     "non positive",
			duration: 0,
			expected: "1 milliseconds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := postgresInterval(tt.duration); got != tt.expected {
				t.Fatalf("postgresInterval() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestClaimTokenIncludesWorkerAndIsUnique(t *testing.T) {
	t.Parallel()

	const workerID = "worker-a"

	notBefore := time.Now().Add(-time.Minute).UnixNano()
	notAfter := time.Now().Add(time.Minute).UnixNano()

	seen := make(map[string]struct{}, 64)
	entropySeen := make(map[string]struct{}, 64)
	for range 64 {
		token := newClaimToken(workerID)

		parts := strings.SplitN(token, ":", 3)
		if len(parts) != 3 || parts[0] != workerID {
			t.Fatalf("newClaimToken() = %q, want %s:<unixnano>:<random>", token, workerID)
		}

		createdAt, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			t.Fatalf("claim token timestamp %q is not an int64: %v", parts[1], err)
		}
		if createdAt < notBefore || createdAt > notAfter {
			t.Fatalf("claim token timestamp %d is not current, want between %d and %d", createdAt, notBefore, notAfter)
		}

		random, err := hex.DecodeString(parts[2])
		if err != nil || len(random) != 16 {
			t.Fatalf("claim token random part %q is not 16 hex-encoded bytes: %v", parts[2], err)
		}

		if _, ok := entropySeen[parts[2]]; ok {
			t.Fatalf("newClaimToken() repeated random suffix %q; entropy must vary per token", parts[2])
		}
		entropySeen[parts[2]] = struct{}{}

		if _, ok := seen[token]; ok {
			t.Fatalf("newClaimToken() returned duplicate token %q", token)
		}
		seen[token] = struct{}{}
	}
}

func TestLeaseAndBackoffDefaults(t *testing.T) {
	t.Parallel()

	repo := New(&Config{}, nil, nil)

	if got := repo.cfg.ProcessingLease; got != 30*time.Second {
		t.Fatalf("processingLease() = %s, want 30s", got)
	}
	if got := repo.cfg.RetryBackoff; got != 5*time.Second {
		t.Fatalf("retryBackoff() = %s, want 5s", got)
	}
}

func TestReplayDelay(t *testing.T) {
	tests := []struct {
		name    string
		base    time.Duration
		maximum time.Duration
		round   int
		want    time.Duration
	}{
		// Defaults: base 5m, max 1h.
		{"default round 0", 5 * time.Minute, time.Hour, 0, 5 * time.Minute},
		{"default round 1", 5 * time.Minute, time.Hour, 1, 10 * time.Minute},
		{"default round 2", 5 * time.Minute, time.Hour, 2, 20 * time.Minute},
		{"default round 3", 5 * time.Minute, time.Hour, 3, 40 * time.Minute},
		{"default round 4", 5 * time.Minute, time.Hour, 4, time.Hour},
		{"default round 5", 5 * time.Minute, time.Hour, 5, time.Hour},
		{"default round 6", 5 * time.Minute, time.Hour, 6, time.Hour},

		// base 1ms, max 1h supports beyond round 5 and saturates.
		{"1ms round 0", time.Millisecond, time.Hour, 0, time.Millisecond},
		{"1ms round 5", time.Millisecond, time.Hour, 5, 32 * time.Millisecond},
		{"1ms round 6", time.Millisecond, time.Hour, 6, 64 * time.Millisecond},
		{"1ms round 10", time.Millisecond, time.Hour, 10, 1024 * time.Millisecond},
		{"1ms round 20", time.Millisecond, time.Hour, 20, 1048576 * time.Millisecond},
		{"1ms round 21", time.Millisecond, time.Hour, 21, 2097152 * time.Millisecond},
		{"1ms round 22", time.Millisecond, time.Hour, 22, time.Hour},
		{"1ms round 30", time.Millisecond, time.Hour, 30, time.Hour},
		{"1ms round 60", time.Millisecond, time.Hour, 60, time.Hour},

		// Equal base and maximum.
		{"equal round 0", 5 * time.Minute, 5 * time.Minute, 0, 5 * time.Minute},
		{"equal round 1", 5 * time.Minute, 5 * time.Minute, 1, 5 * time.Minute},
		{"equal round 5", 5 * time.Minute, 5 * time.Minute, 5, 5 * time.Minute},
		{"equal round 63", 5 * time.Minute, 5 * time.Minute, 63, 5 * time.Minute},

		// Maximum near int64 overflow.
		{"maxint round 0", time.Duration(1) << 62, time.Duration(math.MaxInt64), 0, time.Duration(1) << 62},
		{"maxint round 1", time.Duration(1) << 62, time.Duration(math.MaxInt64), 1, time.Duration(math.MaxInt64)},
		{"maxint round 63", time.Duration(1) << 62, time.Duration(math.MaxInt64), 63, time.Duration(math.MaxInt64)},

		// Maximum not a power-of-two multiple.
		{"nonpow2 round 0", 3 * time.Millisecond, 10 * time.Millisecond, 0, 3 * time.Millisecond},
		{"nonpow2 round 1", 3 * time.Millisecond, 10 * time.Millisecond, 1, 6 * time.Millisecond},
		{"nonpow2 round 2", 3 * time.Millisecond, 10 * time.Millisecond, 2, 10 * time.Millisecond},
		{"nonpow2 round 3", 3 * time.Millisecond, 10 * time.Millisecond, 3, 10 * time.Millisecond},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := replayDelay(tc.base, tc.maximum, tc.round)
			if got != tc.want {
				t.Errorf("replayDelay(%v, %v, %d) = %v, want %v", tc.base, tc.maximum, tc.round, got, tc.want)
			}
		})
	}
}

func TestPublishRejectsInvalidProgrammaticTiming(t *testing.T) {
	for _, cfg := range []Config{
		{ReplayInterval: -1},
		{ReplayInterval: time.Microsecond},
		{ReplayInterval: time.Hour, ReplayMaxInterval: time.Minute},
		{BatchSize: -1},
		{MaxAttempts: -1},
		{RetryBackoff: -1},
		{ProcessingLease: -1},
	} {
		repo := New(&cfg, nil, nil)
		count, err := repo.PublishTopic(t.Context(), "workflows", zap.NewNop())
		if count != 0 || status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid config performed I/O: count=%d err=%v", count, err)
		}
	}
	// Direct stale-claim completion must validate timing too.
	repo := New(&Config{ReplayInterval: -1}, nil, nil)
	if err := repo.markPublished(t.Context(), &Event{Topic: "workflows"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid mark timing accepted: %v", err)
	}
}
