//nolint:testpackage // Tests unexported query helper behavior without widening production API.
package outboxrelay

import (
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"
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

	if got := repo.processingLease(); got != 30*time.Second {
		t.Fatalf("processingLease() = %s, want 30s", got)
	}
	if got := repo.retryBackoff(); got != 5*time.Second {
		t.Fatalf("retryBackoff() = %s, want 5s", got)
	}
}
