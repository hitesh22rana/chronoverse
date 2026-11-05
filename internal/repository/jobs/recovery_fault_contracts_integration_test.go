//nolint:testpackage // Exercises recovery rollback using PostgreSQL triggers.
package jobs

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

//nolint:gocyclo // Keeps failed takeover and replay rollback assertions together.
func TestIntegrationRecoveryFaultsPreserveLeaseAndPermitRetry(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	for _, fault := range []struct {
		name, table, event string
		deferred, replay   bool
	}{
		{name: "reservation", table: "command_idempotency_keys", event: "INSERT"},
		{name: "takeover", table: "jobs", event: "UPDATE"},
		{name: "completion", table: "command_idempotency_keys", event: "UPDATE"},
		{name: "commit", table: "jobs", event: "UPDATE", deferred: true},
		{name: "replay renewal", table: "jobs", event: "UPDATE", replay: true},
		{name: "replay commit", table: "jobs", event: "UPDATE", deferred: true, replay: true},
	} {
		t.Run(fault.name, func(t *testing.T) {
			fixture := seedClaimedJob(ctx, t, pg, repo)
			setLeaseMinutes(ctx, t, pg, fixture.JobID, -expiredFixtureMinutes)
			process, command := uuid.NewString(), "recovery-fault-"+fixtureTag()
			if fault.replay {
				first, err := repo.RecoverExpiredJobLeases(ctx, 100, recoveryWorkerID, process, command, recoveryLeaseDuration)
				if err != nil {
					t.Fatal(err)
				}
				if recoveredJobByID(first, fixture.JobID) == nil {
					t.Fatalf("initial recovery omitted fixture: %+v", first)
				}
			}
			before := readLeaseOwnership(ctx, t, pg, fixture.JobID)
			ledgerBefore, existsBefore := readWorkerCommand(ctx, t, pg, process, command)
			fields := map[string]string{"id": fixture.JobID}
			if fault.table == "command_idempotency_keys" {
				fields = map[string]string{"scope": commandidempotency.WorkerScope(process), "operation": commandidempotency.OperationJobRecoverExpiredLeases, "idempotency_key": command}
			}
			remove := scopedJobFaultTrigger(ctx, t, pg, fault.table, fault.event, fault.deferred, fields)
			result, err := repo.RecoverExpiredJobLeases(ctx, 100, recoveryWorkerID, process, command, recoveryLeaseDuration)
			if result != nil || status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "job contract fault") {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if after := readLeaseOwnership(ctx, t, pg, fixture.JobID); !reflect.DeepEqual(after, before) {
				t.Fatalf("failed recovery changed authority: %+v -> %+v", before, after)
			}
			ledgerAfter, existsAfter := readWorkerCommand(ctx, t, pg, process, command)
			if existsAfter != existsBefore || !reflect.DeepEqual(ledgerAfter, ledgerBefore) {
				t.Fatalf("failed recovery changed ledger: %+v -> %+v", ledgerBefore, ledgerAfter)
			}
			assertNoOutboxEffect(ctx, t, pg, fixture)
			remove()
			retried, err := repo.RecoverExpiredJobLeases(ctx, 100, recoveryWorkerID, process, command, recoveryLeaseDuration)
			if err != nil {
				t.Fatalf("retry: %v", err)
			}
			recovered := recoveredJobByID(retried, fixture.JobID)
			if recovered == nil {
				t.Fatalf("retry omitted fixture: %+v", retried)
			}
			after := readLeaseOwnership(ctx, t, pg, fixture.JobID)
			if after.LeasedBy.String != recoveryWorkerID || after.LeaseProcessInstanceID.String != process || after.LeaseToken.String != recovered.LeaseToken {
				t.Fatalf("retry authority=%+v", after)
			}
			if fault.replay && after.LeaseToken != before.LeaseToken {
				t.Fatalf("replay replaced token: %v -> %v", before.LeaseToken, after.LeaseToken)
			}
		})
	}
}

func TestIntegrationWorkflowTerminationLookupContracts(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	for _, tc := range []struct {
		name, jobID string
		code        codes.Code
	}{
		{name: "missing job", jobID: uuid.NewString()},
		{name: "invalid identifier", jobID: "invalid-uuid", code: codes.InvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := pg.BeginTx(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if rollbackErr := tx.Rollback(context.Background()); rollbackErr != nil {
					t.Errorf("rollback workflow termination lookup: %v", rollbackErr)
				}
			})
			terminated, err := repo.isWorkflowTerminated(ctx, tx, tc.jobID)
			if terminated || status.Code(err) != tc.code {
				t.Fatalf("terminated=%v error=%v", terminated, err)
			}
		})
	}
}

func TestIntegrationRecoveryCanceledBeforeBeginPreservesLease(t *testing.T) {
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	fixture := seedClaimedJob(t.Context(), t, pg, repo)
	setLeaseMinutes(t.Context(), t, pg, fixture.JobID, -expiredFixtureMinutes)
	before := readLeaseOwnership(t.Context(), t, pg, fixture.JobID)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	process, command := uuid.NewString(), "recover-before-begin-"+fixtureTag()
	result, err := repo.RecoverExpiredJobLeases(ctx, 100, recoveryWorkerID, process, command, recoveryLeaseDuration)
	if result != nil || status.Code(err) != codes.Canceled {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if after := readLeaseOwnership(t.Context(), t, pg, fixture.JobID); !reflect.DeepEqual(after, before) {
		t.Fatalf("canceled recovery changed authority: %+v -> %+v", before, after)
	}
	if ledger, ok := readWorkerCommand(t.Context(), t, pg, process, command); ok {
		t.Fatalf("canceled recovery retained ledger: %+v", ledger)
	}
}

func TestRecoveryReplayRejectsMalformedPayloadBeforeDatabase(t *testing.T) {
	repo := New(&Config{}, nil, nil, nil, nil, nil, nil)
	for _, payload := range []string{`[`, `{}`, `[{"ScheduledAt":"invalid-time"}]`} {
		t.Run(payload, func(t *testing.T) {
			result, err := repo.replayExpiredJobRecovery(t.Context(), nil, []byte(payload), "worker", uuid.NewString(), recoveryLeaseDuration)
			if result != nil || status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "failed to decode recovery replay") {
				t.Fatalf("result=%+v error=%v", result, err)
			}
		})
	}
}
