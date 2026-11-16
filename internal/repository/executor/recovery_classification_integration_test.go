//nolint:testpackage // Verifies recovery through real lease claims and persisted settlement.
package executor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	containerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/terminalreason"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
	jobsrepo "github.com/hitesh22rana/chronoverse/internal/repository/jobs"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

func TestIntegrationRecoveryPreservesSystemClassificationAtExhaustion(t *testing.T) {
	for _, tc := range []struct {
		name       string
		code       codes.Code
		userReason string
	}{
		{name: "missing container", code: codes.NotFound},
		{name: "runtime factory failure", code: codes.FailedPrecondition},
		{name: "runtime unavailable", code: codes.Unavailable},
		{name: "already cleaned", code: codes.Unavailable},
		{name: "created never started", code: codes.Unavailable},
		{name: "nonzero workload exit", code: codes.Aborted, userReason: terminalreason.NonZeroExit.String()},
	} {
		for _, attempts := range []int32{1, 3} {
			t.Run(fmt.Sprintf("%s/attempt=%d", tc.name, attempts), func(t *testing.T) {
				pg := testkit.Postgres(t)
				jobs, workflows := newDurableRepositories(pg)
				fixture, expired := seedDurableRecovery(t, pg, jobs, attempts, tc.name == "runtime unavailable")
				repo := newDurableExecutor(fixture, jobs, workflows, nil, &rejectingContainerSvc{})
				configureDurableRecoveryRuntime(t, repo, jobs, fixture, expired, tc.name)
				err := repo.recoverExpiredLease(t.Context(), expired)
				state := readDurableJobState(t.Context(), t, pg, fixture.JobID)
				assertDurableExecutionFailure(t, &state, attempts, tc.code, tc.userReason)
				if attempts == 3 || tc.userReason != "" {
					if status.Code(err) != tc.code {
						t.Fatalf("lost original recovery diagnostic: %v", err)
					}
				} else if err != nil {
					t.Fatalf("system retry was not accepted: %v", err)
				}
				if state.LeaseToken.Valid || repo.handoffs.size() != 0 {
					t.Fatal("accepted settlement retained lease authority")
				}
				if state.Attempts != attempts {
					t.Fatalf("settlement changed attempt count: %d", state.Attempts)
				}
				completed, receiptErr := jobs.GetContainerCleanupReceipt(t.Context(), jobsmodel.CleanupIdentity{
					JobID:           fixture.JobID,
					ContainerID:     fixture.ContainerID,
					RuntimeInstance: fixture.RuntimeInstance,
					IntentID:        fixture.IntentID,
				})
				wantReceipt := tc.name == "missing container" || tc.name == "already cleaned" || tc.name == "created never started" || tc.userReason != ""
				if receiptErr != nil || completed != wantReceipt {
					t.Fatalf("cleanup evidence=%v/%v want %v", completed, receiptErr, wantReceipt)
				}
			})
		}
	}
}

//nolint:gocyclo // Sequential lease→intent→attach→start provenance must stay together to prove the owner boundary.
func seedDurableRecovery(
	t *testing.T,
	pg *postgres.Postgres,
	jobs *jobsrepo.Repository,
	attempts int32,
	runtimeUnavailable bool,
) (*durableFixture, *jobspb.ExpiredJobLease) {
	t.Helper()
	ctx := t.Context()
	tag := uuid.NewString()
	userID := testkit.SeedUser(ctx, t, pg, "recovery-"+tag+"@chronoverse.test")
	nodeID := "recovery-" + tag
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), durableCleanupBound)
		defer cancel()
		if _, err := pg.Exec(cleanupCtx, `DELETE FROM users WHERE id=$1`, userID); err != nil {
			t.Error(err)
		}
		if _, err := pg.Exec(cleanupCtx, `DELETE FROM runtime_nodes WHERE id=$1`, nodeID); err != nil {
			t.Error(err)
		}
	})
	workflowID := testkit.SeedWorkflow(ctx, t, pg, userID, nodeID)
	if _, err := pg.Exec(ctx, `INSERT INTO runtime_nodes(id,node_name,docker_endpoint,status,last_heartbeat_at,max_concurrency)
 VALUES($1,$1,'tcp://recovery:2376','READY',clock_timestamp() AT TIME ZONE 'utc',1)`, nodeID); err != nil {
		t.Fatal(err)
	}
	scheduled := time.Now().UTC().Add(-time.Minute)
	jobID, err := jobs.ScheduleJob(ctx, workflowID, userID, scheduled.Format(time.RFC3339Nano), "AUTOMATIC", uuid.NewString(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, queueErr := pg.Exec(ctx, `UPDATE jobs SET status='QUEUED',dispatch_attempts=1 WHERE id=$1`, jobID); queueErr != nil {
		t.Fatal(queueErr)
	}
	claimed, ok, reason, err := jobs.ClaimJob(ctx, jobID, workflowID, "original-worker", uuid.NewString(), uuid.NewString(), 30*time.Second, 1)
	if err != nil || !ok {
		t.Fatalf("claim: %v/%s", err, reason)
	}
	if claimed.RuntimeNodeID.String == "" || claimed.RuntimeEndpoint.String == "" {
		t.Fatalf("claim did not select a runtime: %+v", claimed)
	}

	if _, setupErr := pg.Exec(ctx, "UPDATE jobs SET attempts=$2 WHERE id=$1", jobID, attempts); setupErr != nil {
		t.Fatal(setupErr)
	}
	claimed.Attempts = attempts
	const runtimeInstance = "test-daemon"
	containerID := strings.Repeat(strings.ReplaceAll(uuid.NewString(), "-", ""), 2)
	ensured, err := jobs.UpdateContainerIntent(ctx, "ensure", &jobsmodel.ContainerIntentCommand{
		JobID:      jobID,
		LeaseToken: claimed.LeaseToken,
		Attempt:    claimed.Attempts, WorkflowGeneration: 1,
		RuntimeInstance: runtimeInstance,
		RequestHash:     uuid.NewString(),
	})
	if err != nil || ensured.Intent == nil {
		t.Fatalf("ensure container intent: %v", err)
	}
	intentID := ensured.Intent.ID
	create, err := jobs.UpdateContainerIntent(ctx, "reserve_create", &jobsmodel.ContainerIntentCommand{
		JobID: jobID, LeaseToken: claimed.LeaseToken, Attempt: claimed.Attempts, IntentID: intentID,
	})
	if err != nil || !create.Granted {
		t.Fatalf("reserve container create: %v granted=%v", err, create != nil && create.Granted)
	}
	if _, err = jobs.UpdateContainerIntent(ctx, "observe", &jobsmodel.ContainerIntentCommand{
		JobID: jobID, LeaseToken: claimed.LeaseToken, Attempt: claimed.Attempts, IntentID: intentID,
		RuntimeInstance: runtimeInstance, ContainerID: containerID,
	}); err != nil {
		t.Fatalf("observe container: %v", err)
	}
	if attachErr := jobs.AttachJobContainer(ctx, jobID, claimed.LeaseToken, containerID, claimed.RuntimeNodeID.String, uuid.NewString()); attachErr != nil {
		t.Fatal(attachErr)
	}
	start, err := jobs.UpdateContainerIntent(ctx, "reserve_start", &jobsmodel.ContainerIntentCommand{
		JobID: jobID, LeaseToken: claimed.LeaseToken, Attempt: claimed.Attempts, IntentID: intentID, ContainerID: containerID,
	})
	if err != nil || !start.Granted {
		t.Fatalf("reserve container start: %v granted=%v", err, start != nil && start.Granted)
	}
	if _, expireErr := pg.Exec(ctx, `UPDATE jobs SET attempts=$2,lease_expires_at=clock_timestamp() AT TIME ZONE 'utc'-interval '1 second' WHERE id=$1`, jobID, attempts); expireErr != nil {
		t.Fatal(expireErr)
	}
	if runtimeUnavailable {
		if _, nodeErr := pg.Exec(ctx, `UPDATE runtime_nodes SET status='UNHEALTHY' WHERE id=$1`, nodeID); nodeErr != nil {
			t.Fatal(nodeErr)
		}
	}
	recovered, err := jobs.RecoverExpiredJobLeases(ctx, 1, "recovery-worker", uuid.NewString(), uuid.NewString(), 30*time.Second)
	if err != nil || len(recovered) != 1 {
		t.Fatalf("recover lease: %v, rows=%d", err, len(recovered))
	}
	require.Equal(t, jobID, recovered[0].ID, "recovery must reclaim the seeded job")
	require.NotEqual(t, claimed.LeaseToken, recovered[0].LeaseToken, "recovery must acquire fresh authority")
	return &durableFixture{
		JobID:           jobID,
		WorkflowID:      workflowID,
		UserID:          userID,
		LeaseToken:      recovered[0].LeaseToken,
		ContainerID:     containerID,
		IntentID:        intentID,
		RuntimeInstance: runtimeInstance,
		LastScheduledAt: scheduled,
		Generation:      1,
		Interval:        1,
	}, recovered[0].ToProto()
}

func configureDurableRecoveryRuntime(
	t *testing.T,
	repo *Repository,
	jobs *jobsrepo.Repository,
	fixture *durableFixture,
	expired *jobspb.ExpiredJobLease,
	name string,
) {
	t.Helper()
	require.Equal(t, fixture.ContainerID, expired.GetContainerId(), "recovery must carry the observed immutable container")
	container := &recoveryContainer{state: containerpkg.State{Status: "exited"}}
	if name == "nonzero workload exit" {
		container.state.ExitCode = 17
	}
	if name == "created never started" {
		container.state.Status = "created"
	}
	if name == "missing container" {
		container.inspectErr = status.Error(codes.NotFound, "container disappeared before its outcome was observed")
	}
	if name == "already cleaned" {
		if err := jobs.RecordContainerCleanupReceipt(t.Context(), jobsmodel.CleanupIdentity{
			JobID:           expired.GetId(),
			ContainerID:     expired.GetContainerId(),
			RuntimeInstance: fixture.RuntimeInstance,
			IntentID:        fixture.IntentID,
		}); err != nil {
			t.Fatal(err)
		}
	}
	repo.svc.CsvcForEndpoint = func(node, endpoint string) (ContainerSvc, error) {
		if node != expired.GetRuntimeNodeId() || endpoint != expired.GetRuntimeEndpoint() {
			t.Fatalf("recovery lost routing: %s/%s", node, endpoint)
		}
		if name == "runtime factory failure" {
			return nil, status.Error(codes.FailedPrecondition, "runtime TLS configuration unavailable")
		}
		if name == "runtime unavailable" || name == "already cleaned" {
			t.Fatal("recovery contacted an unavailable/cleaned runtime")
		}
		return container, nil
	}
}
