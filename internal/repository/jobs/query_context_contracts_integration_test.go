//nolint:testpackage // Verifies cancellation at real PostgreSQL query boundaries.
package jobs

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestIntegrationRecoveryQueryCancellationRollsBackReservation(t *testing.T) {
	repo := newCommitCancellationRepository(t)
	pg := testkit.Postgres(t)
	fixture := seedClaimedJob(t.Context(), t, pg, repo)
	setLeaseMinutes(t.Context(), t, pg, fixture.JobID, -expiredFixtureMinutes)
	before := readLeaseOwnership(t.Context(), t, pg, fixture.JobID)
	process, command := uuid.NewString(), "recover-query-cancel-"+fixtureTag()
	ctx, target := cancelAtQueryContext(t, "WITH", recoverExpiredJobLeasesQuery())
	result, err := repo.RecoverExpiredJobLeases(ctx, 100, recoveryWorkerID, process, command, recoveryLeaseDuration)
	if !target.fired.Load() || ctx.Err() != context.Canceled || result != nil || status.Code(err) != codes.Canceled {
		t.Fatalf("target=%t result=%+v error=%v", target.fired.Load(), result, err)
	}
	if after := readLeaseOwnership(t.Context(), t, pg, fixture.JobID); !reflect.DeepEqual(after, before) {
		t.Fatalf("query cancellation changed authority: %+v -> %+v", before, after)
	}
	if ledger, ok := readWorkerCommand(t.Context(), t, pg, process, command); ok {
		t.Fatalf("query cancellation retained reservation: %+v", ledger)
	}
	assertNoOutboxEffect(t.Context(), t, pg, fixture)
	retry, err := repo.RecoverExpiredJobLeases(t.Context(), 100, recoveryWorkerID, process, command, recoveryLeaseDuration)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if recoveredJobByID(retry, fixture.JobID) == nil {
		t.Fatalf("retry omitted fixture: %+v", retry)
	}
	after := readLeaseOwnership(t.Context(), t, pg, fixture.JobID)
	if after.LeasedBy.String != recoveryWorkerID || after.LeaseProcessInstanceID.String != process {
		t.Fatalf("retry authority=%+v", after)
	}
	assertNoOutboxEffect(t.Context(), t, pg, fixture)
}

func TestIntegrationReleaseTerminationQueryCancellationRollsBackReservation(t *testing.T) {
	repo := newCommitCancellationRepository(t)
	pg := testkit.Postgres(t)
	fixture := seedClaimedJob(t.Context(), t, pg, repo)
	before := readTerminalJobState(t.Context(), t, pg, fixture.JobID)
	slots := readRuntimeRunningJobs(t.Context(), t, pg, before.RuntimeNodeID.String)
	command := "release-query-cancel-" + fixtureTag()
	next := time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)
	ctx, target := cancelAtQueryContext(t, "SELECT", "SELECT w.terminated_at IS NOT NULL")
	err := repo.ReleaseJobForRetry(ctx, fixture.JobID, fixture.LeaseToken, next, "Unavailable", "retry after cancellation", command)
	if !target.fired.Load() || ctx.Err() != context.Canceled || status.Code(err) != codes.Canceled {
		t.Fatalf("target=%t error=%v", target.fired.Load(), err)
	}
	if after := readTerminalJobState(t.Context(), t, pg, fixture.JobID); !reflect.DeepEqual(after, before) {
		t.Fatalf("lookup cancellation changed job: %+v -> %+v", before, after)
	}
	if got := readRuntimeRunningJobs(t.Context(), t, pg, before.RuntimeNodeID.String); got != slots {
		t.Fatalf("canceled lookup slots=%d, want %d", got, slots)
	}
	if ledger, ok := readJobCommand(t.Context(), t, pg, fixture.JobID, commandidempotency.OperationJobReleaseForRetry, command); ok {
		t.Fatalf("lookup cancellation retained reservation: %+v", ledger)
	}
	assertNoOutboxEffect(t.Context(), t, pg, fixture)
	if retryErr := repo.ReleaseJobForRetry(t.Context(), fixture.JobID, fixture.LeaseToken, next, "Unavailable", "retry after cancellation", command); retryErr != nil {
		t.Fatalf("retry: %v", retryErr)
	}
	after := readTerminalJobState(t.Context(), t, pg, fixture.JobID)
	if after.Status != "PENDING" {
		t.Fatalf("retry status=%s, want PENDING", after.Status)
	}
	assertTerminalLeaseReleased(t, after)
	if got := readRuntimeRunningJobs(t.Context(), t, pg, before.RuntimeNodeID.String); got != slots-1 {
		t.Fatalf("retry slots=%d, want %d", got, slots-1)
	}
	assertNoOutboxEffect(t.Context(), t, pg, fixture)
}

func TestIntegrationRenewRecoveryQueryCancellationPreservesAuthority(t *testing.T) {
	repo := newCommitCancellationRepository(t)
	pg := testkit.Postgres(t)
	fixture := seedClaimedJob(t.Context(), t, pg, repo)
	before := readLeaseOwnership(t.Context(), t, pg, fixture.JobID)
	tx, err := repo.pg.BeginTx(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// This helper never reaches COMMIT. Roll back with the uncanceled test context.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		if rollbackErr := tx.Rollback(cleanupCtx); rollbackErr != nil {
			t.Errorf("rollback canceled renewal: %v", rollbackErr)
		}
	})
	ctx, target := cancelAtQueryContext(t, "WITH", renewRecoveryReplayQuery())
	ctx, span := repo.tp.Start(ctx, "test.renewRecoveryReplay")
	defer span.End()
	result, err := repo.renewRecoveryReplay(ctx, tx, []*jobsmodel.ExpiredJobLease{{ID: fixture.JobID, LeaseToken: fixture.LeaseToken}}, "worker", uuid.NewString(), recoveryLeaseDuration)
	if !target.fired.Load() || ctx.Err() != context.Canceled || result != nil || status.Code(err) != codes.Canceled {
		t.Fatalf("target=%t result=%+v error=%v", target.fired.Load(), result, err)
	}
	if after := readLeaseOwnership(t.Context(), t, pg, fixture.JobID); !reflect.DeepEqual(after, before) {
		t.Fatalf("canceled renewal changed authority: %+v -> %+v", before, after)
	}
}

func TestIntegrationCancelCanceledBeforeBeginPreservesSnapshot(t *testing.T) {
	repo := newTestRepository(t)
	pg := testkit.Postgres(t)
	fixture := seedClaimedJob(t.Context(), t, pg, repo)
	before := readTerminalJobState(t.Context(), t, pg, fixture.JobID)
	command := "cancel-before-begin-" + fixtureTag()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := repo.CancelJob(ctx, fixture.JobID, command, "OPERATOR_REQUEST")
	if result != nil || status.Code(err) != codes.Canceled {
		t.Fatalf("snapshot=%+v error=%v", result, err)
	}
	if after := readTerminalJobState(t.Context(), t, pg, fixture.JobID); !reflect.DeepEqual(after, before) {
		t.Fatalf("canceled caller changed job: %+v -> %+v", before, after)
	}
	if ledger, ok := readJobCommand(t.Context(), t, pg, fixture.JobID, commandidempotency.OperationJobCancel, command); ok {
		t.Fatalf("canceled caller retained command: %+v", ledger)
	}
	assertNoOutboxEffect(t.Context(), t, pg, fixture)
	retry, err := repo.CancelJob(t.Context(), fixture.JobID, command, "OPERATOR_REQUEST")
	if err != nil || retry == nil || retry.ID != fixture.JobID || retry.PreviousStatus != "RUNNING" {
		t.Fatalf("retry=%+v error=%v", retry, err)
	}
}
