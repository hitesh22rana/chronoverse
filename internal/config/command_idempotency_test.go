//nolint:testpackage // Tests unexported configuration validation directly.
package config

import (
	"testing"
	"time"

	"github.com/kelseyhightower/envconfig"

	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
)

func TestCommandIdempotencyEventRetention(t *testing.T) {
	t.Parallel()

	if got := commandidempotency.DefaultEventCommandRetention; got != 336*time.Hour {
		t.Fatalf("default event retention = %s, want 336h", got)
	}
	if err := (CommandIdempotency{EventRetention: commandidempotency.MinimumEventCommandRetention}).validate(); err != nil {
		t.Fatalf("minimum event retention rejected: %v", err)
	}
	if err := (CommandIdempotency{EventRetention: commandidempotency.MinimumEventCommandRetention - time.Second}).validate(); err == nil {
		t.Fatal("event retention below the supported replay window was accepted")
	}
}

func TestCommandIdempotencyDefaultConfiguration(t *testing.T) {
	var cfg CommandIdempotency
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		t.Fatalf("process command idempotency config: %v", err)
	}
	if cfg.EventRetention != commandidempotency.DefaultEventCommandRetention {
		t.Fatalf(
			"configured event retention = %s, want %s",
			cfg.EventRetention,
			commandidempotency.DefaultEventCommandRetention,
		)
	}
}

func TestOutboxRelayIdempotencyCleanupBatchValidation(t *testing.T) {
	enabled := OutboxRelayConfig{
		CleanupEnabled:               true,
		IdempotencyCleanupMaxBatches: 0,
	}
	if err := enabled.validate(); err == nil {
		t.Fatal("enabled cleanup accepted a non-positive idempotency batch count")
	}

	disabled := OutboxRelayConfig{
		CleanupEnabled:               false,
		IdempotencyCleanupMaxBatches: 0,
	}
	if err := disabled.validate(); err != nil {
		t.Fatalf("disabled cleanup rejected the ignored batch count: %v", err)
	}
}
