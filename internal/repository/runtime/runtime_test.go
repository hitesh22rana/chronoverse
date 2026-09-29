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

func TestReconcileRunningJobsQueryCountsOnlyRunningOwnedJobs(t *testing.T) {
	query := reconcileRunningJobsQuery()

	assertRuntimeQueryContains(t, query, "UPDATE runtime_nodes AS rn")
	assertRuntimeQueryContains(t, query, "SET running_jobs = (")
	assertRuntimeQueryContains(t, query, "SELECT COUNT(*)::int")
	assertRuntimeQueryContains(t, query, "FROM jobs AS j")
	assertRuntimeQueryContains(t, query, "j.status = 'RUNNING'")
	assertRuntimeQueryContains(t, query, "j.runtime_node_id = rn.id")
	assertRuntimeQueryContains(t, query, "WHERE rn.id = $1")

	if strings.Contains(query, "status = 'READY'") || strings.Contains(query, "status = 'DRAINING'") {
		t.Fatalf("reconciliation must not rewrite runtime status:\n%s", query)
	}
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

func assertRuntimeQueryContains(t *testing.T, value, want string) {
	t.Helper()

	if !strings.Contains(value, want) {
		t.Fatalf("expected query to contain %q:\n%s", want, value)
	}
}
