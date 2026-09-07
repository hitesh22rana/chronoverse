//nolint:testpackage // These tests cover unexported executor config normalization.
package executor

import (
	"runtime"
	"testing"
)

func TestNormalizeConfigConcurrency(t *testing.T) {
	t.Parallel()

	autoConcurrency := runtime.GOMAXPROCS(0)
	if autoConcurrency < 1 {
		autoConcurrency = 1
	}

	tests := []struct {
		name string
		cfg  *Config
		want int
	}{
		{
			name: "nil config uses auto concurrency",
			cfg:  nil,
			want: autoConcurrency,
		},
		{
			name: "zero concurrency uses auto concurrency",
			cfg:  &Config{Concurrency: 0},
			want: autoConcurrency,
		},
		{
			name: "negative concurrency uses auto concurrency",
			cfg:  &Config{Concurrency: -1},
			want: autoConcurrency,
		},
		{
			name: "positive concurrency is explicit",
			cfg:  &Config{Concurrency: 3},
			want: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := normalizeConfig(tt.cfg)
			if err != nil {
				t.Fatalf("normalizeConfig() error = %v", err)
			}
			if got.Concurrency != tt.want {
				t.Fatalf("normalizeConfig().Concurrency = %d, want %d", got.Concurrency, tt.want)
			}
		})
	}
}

func TestNormalizeConfigRejectsReconciliationLimitBelowConcurrency(t *testing.T) {
	t.Parallel()

	_, err := normalizeConfig(&Config{
		Concurrency:                 4,
		AwaitingReconciliationLimit: 3,
	})
	if err == nil {
		t.Fatal("normalizeConfig() error = nil, want invalid reconciliation limit")
	}
}

func TestNormalizeConcurrencyFallbackHasFloor(t *testing.T) {
	t.Parallel()

	cfg := &Config{Concurrency: 0}
	normalizeConcurrency(cfg)

	if cfg.Concurrency < 1 {
		t.Fatalf("normalizeConcurrency().Concurrency = %d, want at least 1", cfg.Concurrency)
	}
}

func TestSystemRetryBackoffIsBoundedExponential(t *testing.T) {
	t.Parallel()

	r := &Repository{cfg: defaultConfig()}
	// default backoff is 30s: 30, 60, 120, 240, capped at 240.
	want := map[int32]int64{
		1:  30,
		2:  60,
		3:  120,
		4:  240,
		5:  240,
		10: 240,
	}
	for attempt, seconds := range want {
		if got := r.systemRetryBackoff(attempt); int64(got.Seconds()) != seconds {
			t.Fatalf("systemRetryBackoff(%d) = %v, want %ds", attempt, got, seconds)
		}
	}
}
