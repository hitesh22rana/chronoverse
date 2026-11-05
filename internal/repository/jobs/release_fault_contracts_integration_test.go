//nolint:testpackage // Reuses real lease and command-ledger fixtures.
package jobs

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

//nolint:gocyclo // Verifies both release outcomes against the same fault matrix.
func TestIntegrationReleaseFaultsRollbackAndPermitSameCommandRetry(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	for _, terminated := range []bool{false, true} {
		name := "retry"
		if terminated {
			name = "terminated"
		}
		t.Run(name, func(t *testing.T) {
			for _, fault := range []struct {
				name, table, event string
				deferred           bool
			}{
				{name: "reservation", table: "command_idempotency_keys", event: "INSERT"},
				{name: "job update", table: "jobs", event: "UPDATE"},
				{name: "runtime decrement", table: "runtime_nodes", event: "UPDATE"},
				{name: "completion", table: "command_idempotency_keys", event: "UPDATE"},
				{name: "commit", table: "jobs", event: "UPDATE", deferred: true},
			} {
				t.Run(fault.name, func(t *testing.T) {
					fixture := seedClaimedJob(ctx, t, pg, repo)
					if terminated {
						if _, err := pg.Exec(ctx, `UPDATE workflows SET terminated_at=now() AT TIME ZONE 'utc' WHERE id=$1`, fixture.WorkflowID); err != nil {
							t.Fatal(err)
						}
					}
					before := readTerminalJobState(ctx, t, pg, fixture.JobID)
					slots := readRuntimeRunningJobs(ctx, t, pg, before.RuntimeNodeID.String)
					command := "release-fault-" + fixtureTag()
					fields := map[string]string{"id": fixture.JobID}
					switch fault.table {
					case "runtime_nodes":
						fields = map[string]string{"id": before.RuntimeNodeID.String}
					case "command_idempotency_keys":
						fields = map[string]string{"scope": commandidempotency.JobScope(fixture.JobID), "operation": commandidempotency.OperationJobReleaseForRetry, "idempotency_key": command}
					}
					remove := scopedJobFaultTrigger(ctx, t, pg, fault.table, fault.event, fault.deferred, fields)
					next := time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)
					invoke := func() error {
						return repo.ReleaseJobForRetry(ctx, fixture.JobID, fixture.LeaseToken, next, "Unavailable", "retry after fault", command)
					}
					if err := invoke(); status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "job contract fault") {
						t.Fatalf("fault error=%v", err)
					}
					if after := readTerminalJobState(ctx, t, pg, fixture.JobID); !reflect.DeepEqual(after, before) {
						t.Fatalf("fault changed job: %+v -> %+v", before, after)
					}
					if got := readRuntimeRunningJobs(ctx, t, pg, before.RuntimeNodeID.String); got != slots {
						t.Fatalf("fault slots=%d, want %d", got, slots)
					}
					if ledger, ok := readJobCommand(ctx, t, pg, fixture.JobID, commandidempotency.OperationJobReleaseForRetry, command); ok {
						t.Fatalf("fault retained ledger: %+v", ledger)
					}
					assertNoOutboxEffect(ctx, t, pg, fixture)
					remove()
					if err := invoke(); err != nil {
						t.Fatalf("retry: %v", err)
					}
					final := readTerminalJobState(ctx, t, pg, fixture.JobID)
					wantStatus := "PENDING"
					if terminated {
						wantStatus = "CANCELED"
						assertNullString(t, "terminal reason", final.TerminalReasonCode, "WORKFLOW_TERMINATED")
					}
					if final.Status != wantStatus {
						t.Fatalf("status=%s, want %s", final.Status, wantStatus)
					}
					assertTerminalLeaseReleased(t, final)
					if got := readRuntimeRunningJobs(ctx, t, pg, before.RuntimeNodeID.String); got != slots-1 {
						t.Fatalf("retry slots=%d, want %d", got, slots-1)
					}
					if err := invoke(); err != nil {
						t.Fatalf("replay: %v", err)
					}
					if after := readTerminalJobState(ctx, t, pg, fixture.JobID); !reflect.DeepEqual(after, final) {
						t.Fatalf("replay changed job: %+v -> %+v", final, after)
					}
					assertNoOutboxEffect(ctx, t, pg, fixture)
				})
			}
		})
	}
}

func TestIntegrationTerminatedReleaseRejectsStaleLeaseWithoutEffects(t *testing.T) {
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	fixture := seedClaimedJob(t.Context(), t, pg, repo)
	if _, err := pg.Exec(t.Context(), `UPDATE workflows SET terminated_at=now() AT TIME ZONE 'utc' WHERE id=$1`, fixture.WorkflowID); err != nil {
		t.Fatal(err)
	}
	before := readTerminalJobState(t.Context(), t, pg, fixture.JobID)
	command := "terminated-stale-" + fixtureTag()
	err := repo.ReleaseJobForRetry(t.Context(), fixture.JobID, "stale-lease", time.Now().UTC().Format(time.RFC3339Nano), "Unavailable", "retry", command)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("error=%v, want stale lease rejection", err)
	}
	if after := readTerminalJobState(t.Context(), t, pg, fixture.JobID); !reflect.DeepEqual(after, before) {
		t.Fatalf("stale release changed job: %+v -> %+v", before, after)
	}
	if ledger, ok := readJobCommand(t.Context(), t, pg, fixture.JobID, commandidempotency.OperationJobReleaseForRetry, command); ok {
		t.Fatalf("rejected command retained ledger: %+v", ledger)
	}
	assertNoOutboxEffect(t.Context(), t, pg, fixture)
}
