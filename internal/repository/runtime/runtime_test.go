//nolint:testpackage // Tests unexported SQL helpers directly.
package runtime

import (
	"strings"
	"testing"
	"time"
)

func TestLockRuntimeNodeQueryLocksRuntimeRow(t *testing.T) {
	assertRuntimeQueryContains(t, lockRuntimeNodeQuery(), "FOR UPDATE")
}

func TestUpsertRuntimeNodeQueryRefreshesHeartbeatOnlyWhenRequested(t *testing.T) {
	query := upsertRuntimeNodeQuery()

	assertRuntimeQueryContains(t, query, "$4::runtime_node_status")
	assertRuntimeQueryContains(t, query, "CASE WHEN $7 THEN now() AT TIME ZONE 'utc' ELSE $8::timestamp END")
	assertRuntimeQueryContains(t, query, "status = EXCLUDED.status")
	assertRuntimeQueryContains(t, query, "last_heartbeat_at = CASE")
	assertRuntimeQueryContains(t, query, "WHEN $7 THEN EXCLUDED.last_heartbeat_at")
	assertRuntimeQueryContains(t, query, "ELSE runtime_nodes.last_heartbeat_at")
}

func TestStaleRuntimeHeartbeatAtUsesUnixEpochUTC(t *testing.T) {
	if got, want := staleRuntimeHeartbeatAt(), time.Unix(0, 0).UTC(); !got.Equal(want) {
		t.Fatalf("staleRuntimeHeartbeatAt() = %v, want %v", got, want)
	}
}

func TestNewDefaultsMaxConcurrencyToOne(t *testing.T) {
	repo := New(Config{}, nil)
	if repo.cfg.MaxConcurrency != 1 {
		t.Fatalf("MaxConcurrency = %d, want 1", repo.cfg.MaxConcurrency)
	}
}

func assertRuntimeQueryContains(t *testing.T, value, want string) {
	t.Helper()

	if !strings.Contains(strings.Join(strings.Fields(value), " "), strings.Join(strings.Fields(want), " ")) {
		t.Fatalf("expected query to contain %q:\n%s", want, value)
	}
}
