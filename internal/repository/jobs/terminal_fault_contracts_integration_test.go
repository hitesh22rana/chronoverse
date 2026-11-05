//nolint:testpackage // Reuses real lease and command-ledger fixtures.
package jobs

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

// Every fault is injected by PostgreSQL after an actual write. Retrying the same
// command proves the failed transaction released both the lease and ledger locks.
//
//nolint:gocyclo // Keeps each scoped fault with its rollback and retry assertions.
func TestIntegrationTerminalFaultsRollbackAndPermitSameCommandRetry(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	for _, operation := range terminalCommandOperations {
		t.Run(operation.Name, func(t *testing.T) {
			for _, fault := range []struct {
				name, table, event string
				deferred           bool
			}{
				{name: "reservation", table: "command_idempotency_keys", event: "INSERT"},
				{name: "terminal update", table: "jobs", event: "UPDATE"},
				{name: "runtime decrement", table: "runtime_nodes", event: "UPDATE"},
				{name: "completion", table: "command_idempotency_keys", event: "UPDATE"},
				{name: "commit", table: "jobs", event: "UPDATE", deferred: true},
				{name: "outbox", table: "outbox_events", event: "INSERT"},
			} {
				if operation.WantStatus == "CANCELED" && fault.name == "outbox" {
					continue
				}
				t.Run(fault.name, func(t *testing.T) {
					fixture := seedClaimedJob(ctx, t, pg, repo)
					before := readTerminalJobState(ctx, t, pg, fixture.JobID)
					slots := readRuntimeRunningJobs(ctx, t, pg, before.RuntimeNodeID.String)
					command := "terminal-fault-" + fixtureTag()
					fields := map[string]string{"id": fixture.JobID}
					switch fault.table {
					case "runtime_nodes":
						fields = map[string]string{"id": before.RuntimeNodeID.String}
					case "outbox_events":
						fields = map[string]string{"kafka_key": fixture.WorkflowID}
					case "command_idempotency_keys":
						fields = map[string]string{"scope": commandidempotency.JobScope(fixture.JobID), "operation": operation.Operation, "idempotency_key": command}
					}
					remove := scopedJobFaultTrigger(ctx, t, pg, fault.table, fault.event, fault.deferred, fields)
					err := operation.Invoke(ctx, repo, fixture.JobID, fixture.LeaseToken, command)
					if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "job contract fault") {
						t.Fatalf("fault error=%v", err)
					}
					after := readTerminalJobState(ctx, t, pg, fixture.JobID)
					if !reflect.DeepEqual(after, before) {
						t.Fatalf("failed command changed job: before=%+v after=%+v", before, after)
					}
					if got := readRuntimeRunningJobs(ctx, t, pg, before.RuntimeNodeID.String); got != slots {
						t.Fatalf("runtime slots=%d, want %d", got, slots)
					}
					assertNoOutboxEffect(ctx, t, pg, fixture)
					if ledger, ok := readJobCommand(ctx, t, pg, fixture.JobID, operation.Operation, command); ok {
						t.Fatalf("failed command retained ledger row: %+v", ledger)
					}
					remove()
					if err = operation.Invoke(ctx, repo, fixture.JobID, fixture.LeaseToken, command); err != nil {
						t.Fatalf("retry: %v", err)
					}
					final := readTerminalJobState(ctx, t, pg, fixture.JobID)
					if final.Status != operation.WantStatus {
						t.Fatalf("retry status=%s, want %s", final.Status, operation.WantStatus)
					}
					assertTerminalLeaseReleased(t, final)
					assertTerminalReason(t, operation, final)
					if got := readRuntimeRunningJobs(ctx, t, pg, before.RuntimeNodeID.String); got != slots-1 {
						t.Fatalf("retry slots=%d, want %d", got, slots-1)
					}
					assertOutboxEffectCount(ctx, t, pg, fixture, operation.Operation)
					if err = operation.Invoke(ctx, repo, fixture.JobID, fixture.LeaseToken, command); err != nil {
						t.Fatalf("replay: %v", err)
					}
					assertOutboxEffectCount(ctx, t, pg, fixture, operation.Operation)
				})
			}
		})
	}
}
