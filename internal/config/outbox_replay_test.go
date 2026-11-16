package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hitesh22rana/chronoverse/internal/config"
)

func TestOutboxReplayStartupValidation(t *testing.T) {
	for _, tc := range []struct {
		name, key, value string
		invalid          bool
	}{
		{name: "default"},
		{name: "small base", key: "OUTBOX_RELAY_REPLAY_INTERVAL", value: "1ms"},
		{name: "zero means default", key: "OUTBOX_RELAY_REPLAY_INTERVAL", value: "0"},
		{name: "negative base", key: "OUTBOX_RELAY_REPLAY_INTERVAL", value: "-1s", invalid: true},
		{name: "submillisecond base", key: "OUTBOX_RELAY_REPLAY_INTERVAL", value: "1us", invalid: true},
		{name: "fractional max", key: "OUTBOX_RELAY_REPLAY_MAX_INTERVAL", value: "1h1ns", invalid: true},
		{name: "inverted cap", key: "OUTBOX_RELAY_REPLAY_MAX_INTERVAL", value: "1m", invalid: true},
		{name: "zero batch", key: "OUTBOX_RELAY_BATCH_SIZE", value: "0", invalid: true},
		{name: "negative attempts", key: "OUTBOX_RELAY_MAX_ATTEMPTS", value: "-1", invalid: true},
		{name: "zero poll", key: "OUTBOX_RELAY_POLL_INTERVAL", value: "0", invalid: true},
		{name: "zero timeout", key: "OUTBOX_RELAY_CONTEXT_TIMEOUT", value: "0", invalid: true},
		{name: "short lease", key: "OUTBOX_RELAY_PROCESSING_LEASE", value: "1us", invalid: true},
		{name: "negative backoff", key: "OUTBOX_RELAY_RETRY_BACKOFF", value: "-1s", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initializerEnvironment(t)
			if tc.key != "" {
				t.Setenv(tc.key, tc.value)
			}
			cfg, err := config.InitOutboxRelayConfig()
			if tc.invalid {
				require.Error(t, err)
				require.Nil(t, cfg)
				return
			}
			require.NoError(t, err)
			require.Equal(t, time.Hour, cfg.ReplayMaxInterval)
			if tc.name == "small base" {
				require.Equal(t, time.Millisecond, cfg.ReplayInterval)
			} else {
				require.Equal(t, 5*time.Minute, cfg.ReplayInterval)
			}
		})
	}
}
