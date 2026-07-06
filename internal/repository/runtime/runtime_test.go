//nolint:testpackage // Tests unexported SQL helpers directly.
package runtime

import (
	"strings"
	"testing"
)

func TestLockRuntimeNodeQueryLocksRuntimeRow(t *testing.T) {
	query := lockRuntimeNodeQuery()

	assertRuntimeQueryContains(t, query, "FROM runtime_nodes")
	assertRuntimeQueryContains(t, query, "WHERE id = $1")
	assertRuntimeQueryContains(t, query, "FOR UPDATE")
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

func assertRuntimeQueryContains(t *testing.T, value, want string) {
	t.Helper()

	if !strings.Contains(value, want) {
		t.Fatalf("expected query to contain %q:\n%s", want, value)
	}
}
