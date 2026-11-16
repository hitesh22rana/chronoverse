//nolint:testpackage // Exercise real job authority and PostgreSQL transitions.
package jobs

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/google/uuid"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

//nolint:gocyclo // One owner-boundary scenario proves authority, settlement and capacity transfer together.
func TestIntegrationContainerIntentGrantsClosureAndCapacity(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	user, workflow := seedUserWorkflow(ctx, t, pg)
	job, lease := seedRunningJob(ctx, t, pg, repo, workflow, user, t.Name())
	command := &jobsmodel.ContainerIntentCommand{JobID: job, LeaseToken: lease, Attempt: 1, WorkflowGeneration: 1, RuntimeInstance: "daemon-original", RequestHash: "frozen-request"}
	ensured, err := repo.UpdateContainerIntent(ctx, "ensure", command)
	if err != nil {
		t.Fatal(err)
	}
	command.IntentID = ensured.Intent.ID
	for _, operation := range []string{"reserve_create"} {
		result, grantErr := repo.UpdateContainerIntent(ctx, operation, command)
		if grantErr != nil || !result.Granted {
			t.Fatalf("first grant: %+v %v", result, grantErr)
		}
		result, grantErr = repo.UpdateContainerIntent(ctx, operation, command)
		if grantErr != nil || result.Granted {
			t.Fatalf("replayed grant: %+v %v", result, grantErr)
		}
	}
	command.ContainerID = strings.Repeat("a", 64)
	if _, err = repo.UpdateContainerIntent(ctx, "observe", command); err != nil {
		t.Fatal(err)
	}
	if err = repo.AttachJobContainer(ctx, job, lease, command.ContainerID, ensured.Intent.RuntimeNodeID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	start, err := repo.UpdateContainerIntent(ctx, "reserve_start", command)
	if err != nil || !start.Granted {
		t.Fatalf("start grant %+v %v", start, err)
	}
	repeated, err := repo.UpdateContainerIntent(ctx, "reserve_start", command)
	if err != nil || repeated.Granted {
		t.Fatalf("start replay %+v %v", repeated, err)
	}
	if err = repo.FailJob(ctx, job, lease, "SYSTEM", "Unavailable", "runtime unknown", "SYSTEM_ERROR", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	// Registration/heartbeat execute this same locked fresh-view reconciliation.
	if _, err = pg.Exec(ctx, "SELECT reconcile_runtime_capacity($1)", ensured.Intent.RuntimeNodeID); err != nil {
		t.Fatal(err)
	}
	var occupancy int
	if err = pg.QueryRow(ctx, "SELECT running_jobs FROM runtime_nodes WHERE id=$1", ensured.Intent.RuntimeNodeID).Scan(&occupancy); err != nil || occupancy != 1 {
		t.Fatalf("held occupancy %d %v", occupancy, err)
	}
	var blocked bool
	if err = pg.QueryRow(ctx, "SELECT workflow_container_blocked($1)", workflow).Scan(&blocked); err != nil || !blocked {
		t.Fatalf("workflow gate %v %v", blocked, err)
	}
	nextJob, scheduleErr := repo.ScheduleJob(ctx, workflow, user, time.Now().UTC().Format(time.RFC3339Nano), "MANUAL", "next-"+uuid.NewString(), 1)
	if scheduleErr != nil {
		t.Fatal(scheduleErr)
	}
	queueJob(ctx, t, pg, nextJob)
	_, admitted, reason, claimErr := repo.ClaimJob(ctx, nextJob, workflow, "next-worker", uuid.NewString(), uuid.NewString(), time.Minute, 1)
	if claimErr != nil || admitted || reason != "job deferred behind another workflow job" {
		t.Fatalf("unresolved predecessor admitted next job: admitted=%v reason=%q error=%v", admitted, reason, claimErr)
	}
	var spent int
	if err = pg.QueryRow(ctx, "SELECT attempts FROM jobs WHERE id=$1", nextJob).Scan(&spent); err != nil || spent != 0 {
		t.Fatalf("blocked job spent an attempt: %d %v", spent, err)
	}
	if err = repo.RecordContainerCleanupReceipt(ctx, job, command.ContainerID, "wrong-daemon", command.IntentID); err == nil {
		t.Fatal("wrong runtime proof accepted")
	}
	for range 2 {
		if err = repo.RecordContainerCleanupReceipt(ctx, job, command.ContainerID, command.RuntimeInstance, command.IntentID); err != nil {
			t.Fatal(err)
		}
	}
	if err = pg.QueryRow(ctx, "SELECT running_jobs FROM runtime_nodes WHERE id=$1", ensured.Intent.RuntimeNodeID).Scan(&occupancy); err != nil || occupancy != 0 {
		t.Fatalf("released occupancy %d %v", occupancy, err)
	}
	if err = pg.QueryRow(ctx, "SELECT workflow_container_blocked($1)", workflow).Scan(&blocked); err != nil || blocked {
		t.Fatalf("workflow remains gated %v %v", blocked, err)
	}
	queueJob(ctx, t, pg, nextJob)
	_, admitted, reason, claimErr = repo.ClaimJob(ctx, nextJob, workflow, "next-worker", uuid.NewString(), uuid.NewString(), time.Minute, 1)
	if claimErr != nil || !admitted {
		t.Fatalf("removal proof did not reopen admission: reason=%q error=%v", reason, claimErr)
	}
	// A later materialization has its own receipt; it cannot inherit Start authority.
	late := *command
	late.ContainerID = strings.Repeat("b", 64)
	if _, err = repo.UpdateContainerIntent(ctx, "observe", &late); err != nil {
		t.Fatal(err)
	}
	var obligations int
	if err = pg.QueryRow(ctx, "SELECT count(*) FROM outbox_events WHERE payload->'ContainerCleanup'->>'ContainerID'=$1", late.ContainerID).Scan(&obligations); err != nil || obligations != 1 {
		t.Fatalf("late cleanup %d %v", obligations, err)
	}
}

func TestIntegrationContainerReservationSerializesWithTermination(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	user, workflow := seedUserWorkflow(ctx, t, pg)
	job, lease := seedRunningJob(ctx, t, pg, repo, workflow, user, t.Name())
	request := &jobsmodel.ContainerIntentCommand{JobID: job, LeaseToken: lease, Attempt: 1, WorkflowGeneration: 1, RuntimeInstance: "daemon-closure", RequestHash: "immutable"}
	ensured, err := repo.UpdateContainerIntent(ctx, "ensure", request)
	if err != nil {
		t.Fatal(err)
	}
	request.IntentID = ensured.Intent.ID
	tx, err := pg.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // Rollback is a no-op after commit.
	if _, err = tx.Exec(ctx, "SELECT id FROM workflows WHERE id=$1 FOR UPDATE", workflow); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		granted, callErr := repo.UpdateContainerIntent(ctx, "reserve_create", request)
		if callErr == nil && granted.Granted {
			callErr = fmt.Errorf("termination raced into a new create grant")
		}
		result <- callErr
	}()
	if _, err = tx.Exec(ctx, "UPDATE workflows SET terminated_at=clock_timestamp() AT TIME ZONE 'utc' WHERE id=$1", workflow); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case grantErr := <-result:
		if status.Code(grantErr) != codes.Aborted {
			t.Fatalf("post-closure grant result: %v", grantErr)
		}
	case <-ctx.Done():
		t.Fatal("closure/reservation failed to join")
	}
	var closed, reserved bool
	err = pg.QueryRow(ctx, "SELECT closed_at IS NOT NULL,create_reserved_at IS NOT NULL FROM container_creation_intents WHERE id=$1", request.IntentID).Scan(&closed, &reserved)
	if err != nil || !closed || reserved {
		t.Fatalf("closure facts %v/%v %v", closed, reserved, err)
	}
}

func TestIntegrationConcurrentContainerCreateHasOneGrant(t *testing.T) {
	ctx := t.Context()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	user, workflow := seedUserWorkflow(ctx, t, pg)
	job, lease := seedRunningJob(ctx, t, pg, repo, workflow, user, t.Name())
	request := &jobsmodel.ContainerIntentCommand{JobID: job, LeaseToken: lease, Attempt: 1, WorkflowGeneration: 1, RuntimeInstance: "daemon-concurrent", RequestHash: "immutable"}
	ensured, err := repo.UpdateContainerIntent(ctx, "ensure", request)
	if err != nil {
		t.Fatal(err)
	}
	request.IntentID = ensured.Intent.ID
	results := make(chan *jobsmodel.ContainerIntentResult, 2)
	errs := make(chan error, 2)
	for range 2 {
		go func() { r, e := repo.UpdateContainerIntent(ctx, "reserve_create", request); results <- r; errs <- e }()
	}
	winners := 0
	for range 2 {
		r, e := <-results, <-errs
		if e != nil {
			t.Fatal(e)
		}
		if r.Granted {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("create winners=%d", winners)
	}
}

func TestIntegrationContainerGrantRequiresOriginalLeaseAndGeneration(t *testing.T) {
	for _, change := range []string{"lease", "generation"} {
		t.Run(change, func(t *testing.T) {
			ctx := t.Context()
			pg := testkit.Postgres(t)
			repo := newTestRepository(t)
			user, workflow := seedUserWorkflow(ctx, t, pg)
			job, lease := seedRunningJob(ctx, t, pg, repo, workflow, user, t.Name())
			command := &jobsmodel.ContainerIntentCommand{JobID: job, LeaseToken: lease, Attempt: 1, WorkflowGeneration: 1, RuntimeInstance: "original-daemon", RequestHash: "immutable"}
			ensured, err := repo.UpdateContainerIntent(ctx, "ensure", command)
			if err != nil {
				t.Fatal(err)
			}
			command.IntentID = ensured.Intent.ID
			if change == "lease" {
				// Same executor process, different lease: process identity alone is insufficient.
				command.LeaseToken = "same-worker:" + uuid.NewString()
				if _, err = pg.Exec(ctx, "UPDATE jobs SET lease_token=$2 WHERE id=$1", job, command.LeaseToken); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err = pg.Exec(ctx, "UPDATE workflows SET generation=generation+1 WHERE id=$1", workflow); err != nil {
					t.Fatal(err)
				}
			}
			result, err := repo.UpdateContainerIntent(ctx, "reserve_create", command)
			if status.Code(err) != codes.Aborted || (result != nil && result.Granted) {
				t.Fatalf("%s reopened authority: %+v %v", change, result, err)
			}
		})
	}
}

func TestIntegrationRemovedContainerCannotAcquireStart(t *testing.T) {
	ctx := t.Context()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	user, workflow := seedUserWorkflow(ctx, t, pg)
	job, lease := seedRunningJob(ctx, t, pg, repo, workflow, user, t.Name())
	command := &jobsmodel.ContainerIntentCommand{JobID: job, LeaseToken: lease, Attempt: 1, WorkflowGeneration: 1, RuntimeInstance: "daemon-removed", RequestHash: "immutable"}
	ensured, err := repo.UpdateContainerIntent(ctx, "ensure", command)
	if err != nil {
		t.Fatal(err)
	}
	command.IntentID = ensured.Intent.ID
	if _, err = repo.UpdateContainerIntent(ctx, "reserve_create", command); err != nil {
		t.Fatal(err)
	}
	command.ContainerID = strings.Repeat("c", 64)
	if _, err = repo.UpdateContainerIntent(ctx, "observe", command); err != nil {
		t.Fatal(err)
	}
	if err = repo.AttachJobContainer(ctx, job, lease, command.ContainerID, ensured.Intent.RuntimeNodeID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err = repo.RecordContainerCleanupReceipt(ctx, job, command.ContainerID, command.RuntimeInstance, command.IntentID); err != nil {
		t.Fatal(err)
	}
	granted, err := repo.UpdateContainerIntent(ctx, "reserve_start", command)
	if status.Code(err) != codes.Aborted || (granted != nil && granted.Granted) {
		t.Fatalf("removed container got start authority: %+v %v", granted, err)
	}
	var holds int
	if err = pg.QueryRow(ctx, "SELECT count(*) FROM runtime_capacity_holds WHERE intent_id=$1", command.IntentID).Scan(&holds); err != nil || holds != 0 {
		t.Fatalf("hold installed after prior cleanup: %d %v", holds, err)
	}
}

func TestIntegrationDiscoveryInterruptionPreservesFailureBudget(t *testing.T) {
	ctx := t.Context()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	user, workflow := seedUserWorkflow(ctx, t, pg)
	job, lease := seedRunningJob(ctx, t, pg, repo, workflow, user, t.Name())
	req := &jobsmodel.ContainerIntentCommand{
		JobID: job, LeaseToken: lease, Attempt: 1, WorkflowGeneration: 1,
		RuntimeInstance: "daemon-interruption", RequestHash: "immutable",
	}
	ensured, err := repo.UpdateContainerIntent(ctx, "ensure", req)
	if err != nil {
		t.Fatal(err)
	}
	req.IntentID = ensured.Intent.ID
	if _, err = repo.UpdateContainerIntent(ctx, "close", req); err != nil {
		t.Fatal(err)
	}
	req.Token = uuid.NewString()
	req.Generation = ensured.Intent.Generation
	if _, err = pg.Exec(ctx, `UPDATE container_creation_intents
 SET inspection_token=$2, inspection_until=(clock_timestamp() AT TIME ZONE 'utc')+interval '45 seconds',
 inspection_errors=2, inspection_error_code='Unavailable', inspection_error_message='original inspection failure'
 WHERE id=$1`, req.IntentID, req.Token); err != nil {
		t.Fatal(err)
	}
	req.Outcome = "INTERRUPTED"
	result, err := repo.UpdateContainerIntent(ctx, "report", req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Intent.InspectionErrors != 2 || result.Intent.Quarantined || result.Intent.Token != "" || result.Intent.InspectionErrorCode != "Unavailable" {
		t.Fatalf("shutdown spent budget or lost diagnostics: %+v", result.Intent)
	}
	// A late report cannot reuse the released inspection lease.
	req.Outcome = "ERROR"
	if _, err = repo.UpdateContainerIntent(ctx, "report", req); status.Code(err) != codes.Aborted {
		t.Fatalf("stale report: %v", err)
	}
}

func TestIntegrationDeletedWorkflowRetainsLateContainerObligation(t *testing.T) {
	ctx := t.Context()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	user, workflow := seedUserWorkflow(ctx, t, pg)
	job, lease := seedRunningJob(ctx, t, pg, repo, workflow, user, t.Name())
	request := &jobsmodel.ContainerIntentCommand{
		JobID: job, LeaseToken: lease, Attempt: 1, WorkflowGeneration: 1,
		RuntimeInstance: "daemon-late-response", RequestHash: "immutable",
	}
	ensured, err := repo.UpdateContainerIntent(ctx, "ensure", request)
	if err != nil {
		t.Fatal(err)
	}
	request.IntentID = ensured.Intent.ID
	grant, err := repo.UpdateContainerIntent(ctx, "reserve_create", request)
	if err != nil || !grant.Granted {
		t.Fatalf("create permission: %+v %v", grant, err)
	}
	// Simulate a granted Create whose daemon reply arrives after source deletion.
	if _, err = pg.Exec(ctx, "DELETE FROM workflows WHERE id=$1", workflow); err != nil {
		t.Fatal(err)
	}
	request.ContainerID = strings.Repeat("c", 64)
	observed, err := repo.UpdateContainerIntent(ctx, "observe", request)
	if err != nil {
		t.Fatal(err)
	}
	if !observed.Intent.Closed || observed.Intent.StartReserved {
		t.Fatalf("late observation regained authority: %+v", observed.Intent)
	}
	var route, instance, intent string
	err = pg.QueryRow(ctx, `SELECT payload->'ContainerCleanup'->>'RuntimeEndpoint',
 payload->'ContainerCleanup'->>'RuntimeInstance', payload->'ContainerCleanup'->>'IntentID'
 FROM outbox_events WHERE event_key=$1`, cleanupEventKey(job, request.ContainerID, request.RuntimeInstance)).Scan(&route, &instance, &intent)
	if err != nil || route != ensured.Intent.RuntimeEndpoint || instance != request.RuntimeInstance || intent != request.IntentID {
		t.Fatalf("late cleanup lost provenance: %q %q %q %v", route, instance, intent, err)
	}
	if _, err = repo.UpdateContainerIntent(ctx, "reserve_start", request); status.Code(err) != codes.Aborted {
		t.Fatalf("deleted source admitted Start: %v", err)
	}
}

func TestIntegrationRemovalReceiptSerializesWithStartReservation(t *testing.T) {
	ctx := t.Context()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	user, workflow := seedUserWorkflow(ctx, t, pg)
	job, lease := seedRunningJob(ctx, t, pg, repo, workflow, user, t.Name())
	id := strings.Repeat("d", 64)
	identity := seedContainerProvenance(ctx, t, pg, repo, job, lease, id)
	var node string
	if err := pg.QueryRow(ctx, "SELECT runtime_node_id FROM jobs WHERE id=$1", job).Scan(&node); err != nil {
		t.Fatal(err)
	}
	if err := repo.AttachJobContainer(ctx, job, lease, id, node, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	lock, err := pg.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(ctx) //nolint:errcheck // Rollback is a no-op after commit.
	if _, err = lock.Exec(ctx, "SELECT id FROM container_creation_intents WHERE id=$1 FOR UPDATE", identity.IntentID); err != nil {
		t.Fatal(err)
	}
	receiptCtx, cancel := context.WithTimeout(ctx, time.Second)
	err = repo.RecordContainerCleanupReceipt(receiptCtx, job, id, identity.RuntimeInstance, identity.IntentID)
	cancel()
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("receipt bypassed the Start authority row lock: %v", err)
	}
	if err = lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	removed, err := repo.GetContainerCleanupReceipt(ctx, job, id, identity.RuntimeInstance, identity.IntentID)
	if err != nil || removed {
		t.Fatalf("blocked receipt committed: completed=%v error=%v", removed, err)
	}
	req := &jobsmodel.ContainerIntentCommand{
		JobID: job, LeaseToken: lease, Attempt: 1, IntentID: identity.IntentID,
		RuntimeInstance: identity.RuntimeInstance, ContainerID: id,
	}
	grant, err := repo.UpdateContainerIntent(ctx, "reserve_start", req)
	if err != nil || !grant.Granted {
		t.Fatalf("Start after interrupted receipt: %+v %v", grant, err)
	}
	if err = repo.RecordContainerCleanupReceipt(ctx, job, id, identity.RuntimeInstance, identity.IntentID); err != nil {
		t.Fatal(err)
	}
	var held int
	if err = pg.QueryRow(ctx, "SELECT count(*) FROM runtime_capacity_holds WHERE intent_id=$1 AND released_at IS NULL", identity.IntentID).Scan(&held); err != nil || held != 0 {
		t.Fatalf("receipt left capacity held: %d %v", held, err)
	}
}
