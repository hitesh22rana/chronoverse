//nolint:testpackage // Tests unexported query helper behavior without widening production API.
package outboxrelay

import (
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

	first, err := newClaimToken("worker-a")
	if err != nil {
		t.Fatalf("newClaimToken() returned error: %v", err)
	}

	second, err := newClaimToken("worker-a")
	if err != nil {
		t.Fatalf("newClaimToken() returned error: %v", err)
	}

	if !strings.HasPrefix(first, "worker-a:") {
		t.Fatalf("newClaimToken() = %q, want worker prefix", first)
	}
	if first == second {
		t.Fatalf("newClaimToken() returned duplicate token %q", first)
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
