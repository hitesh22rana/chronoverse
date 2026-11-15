//nolint:testpackage // These tests cover unexported executor config normalization.
package executor

import (
	"math"
	"runtime"
	"testing"
	"time"
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

func TestNormalizeConfigRejectsInvalidExplicitLeaseTiming(t *testing.T) {
	for _, cfg := range []Config{
		{LeaseDuration: 500 * time.Millisecond},
		{LeaseDuration: 1500 * time.Millisecond},
		{LeaseDuration: -time.Second},
		{LeaseDuration: (time.Duration(math.MaxInt32) + 1) * time.Second},
		{LeaseRenewInterval: -time.Second},
		{LeaseRenewInterval: 30 * time.Second},
		{LeaseDuration: time.Second, LeaseRenewInterval: time.Second - time.Nanosecond},
	} {
		if got, err := normalizeConfig(&cfg); err == nil {
			t.Fatalf("invalid timing accepted: input=%+v result=%+v", cfg, got)
		}
	}
}

func TestNormalizeConfigLeaseDefaultsAndPrecision(t *testing.T) {
	for _, tc := range []struct {
		name               string
		cfg                *Config
		duration, interval time.Duration
	}{
		{"nil defaults", nil, 30 * time.Second, 10 * time.Second},
		{"zero defaults", &Config{}, 30 * time.Second, 10 * time.Second},
		{"implicit interval for short lease", &Config{LeaseDuration: time.Second}, time.Second, 333333333 * time.Nanosecond},
		{"explicit interval", &Config{LeaseDuration: 2 * time.Second, LeaseRenewInterval: 500 * time.Millisecond}, 2 * time.Second, 500 * time.Millisecond},
		{"wire maximum", &Config{LeaseDuration: time.Duration(math.MaxInt32) * time.Second}, time.Duration(math.MaxInt32) * time.Second, 10 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeConfig(tc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got.LeaseDuration != tc.duration || got.LeaseRenewInterval != tc.interval {
				t.Fatalf("lease timing=%v/%v want %v/%v", got.LeaseDuration, got.LeaseRenewInterval, tc.duration, tc.interval)
			}
		})
	}
}

func TestNormalizeConfigRetainsWorkerIdentityAndRecoveryPolicy(t *testing.T) {
	cfg, err := normalizeConfig(&Config{
		WorkerID: "runtime-worker", LeaseDuration: 3 * time.Second, LeaseRenewInterval: time.Second,
		RecoveryInterval: 2 * time.Second, RecoveryBatchSize: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkerID != "runtime-worker" || cfg.RecoveryInterval != 2*time.Second || cfg.RecoveryBatchSize != 7 {
		t.Fatalf("lease configuration changed worker/recovery policy: worker=%q recovery=%s batch=%d", cfg.WorkerID, cfg.RecoveryInterval, cfg.RecoveryBatchSize)
	}
}
