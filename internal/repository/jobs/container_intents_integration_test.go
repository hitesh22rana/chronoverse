//nolint:testpackage // Exercise real job authority and PostgreSQL transitions.
package jobs

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
	ensured, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationEnsure, command)
	if err != nil {
		t.Fatal(err)
	}
	command.IntentID = ensured.Intent.ID
	for _, operation := range []string{jobsmodel.ContainerIntentOperationReserveCreate} {
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
	if _, err = repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationObserve, command); err != nil {
		t.Fatal(err)
	}
	if err = repo.AttachJobContainer(ctx, job, lease, command.ContainerID, ensured.Intent.RuntimeNodeID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	start, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationReserveStart, command)
	if err != nil || !start.Granted {
		t.Fatalf("start grant %+v %v", start, err)
	}
	repeated, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationReserveStart, command)
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
	if err = repo.RecordContainerCleanupReceipt(ctx, jobsmodel.CleanupIdentity{
		JobID:           job,
		ContainerID:     command.ContainerID,
		RuntimeInstance: "wrong-daemon",
		IntentID:        command.IntentID,
	}); err == nil {
		t.Fatal("wrong runtime proof accepted")
	}
	for range 2 {
		if err = repo.RecordContainerCleanupReceipt(ctx, jobsmodel.CleanupIdentity{
			JobID:           job,
			ContainerID:     command.ContainerID,
			RuntimeInstance: command.RuntimeInstance,
			IntentID:        command.IntentID,
		}); err != nil {
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
	if _, err = repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationObserve, &late); err != nil {
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
	ensured, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationEnsure, request)
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
		granted, callErr := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationReserveCreate, request)
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
	err = pg.QueryRow(ctx, `
		SELECT
			closed_at IS NOT NULL,
			create_reserved_at IS NOT NULL
		FROM
			container_creation_intents
		WHERE
			id = $1
	`, request.IntentID).Scan(&closed, &reserved)
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
	ensured, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationEnsure, request)
	if err != nil {
		t.Fatal(err)
	}
	request.IntentID = ensured.Intent.ID
	results := make(chan *jobsmodel.ContainerIntentResult, 2)
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			r, e := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationReserveCreate, request)
			results <- r
			errs <- e
		}()
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
			ensured, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationEnsure, command)
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
			result, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationReserveCreate, command)
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
	ensured, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationEnsure, command)
	if err != nil {
		t.Fatal(err)
	}
	command.IntentID = ensured.Intent.ID
	if _, err = repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationReserveCreate, command); err != nil {
		t.Fatal(err)
	}
	command.ContainerID = strings.Repeat("c", 64)
	if _, err = repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationObserve, command); err != nil {
		t.Fatal(err)
	}
	if err = repo.AttachJobContainer(ctx, job, lease, command.ContainerID, ensured.Intent.RuntimeNodeID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err = repo.RecordContainerCleanupReceipt(ctx, jobsmodel.CleanupIdentity{
		JobID:           job,
		ContainerID:     command.ContainerID,
		RuntimeInstance: command.RuntimeInstance,
		IntentID:        command.IntentID,
	}); err != nil {
		t.Fatal(err)
	}
	granted, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationReserveStart, command)
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
	ensured, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationEnsure, req)
	if err != nil {
		t.Fatal(err)
	}
	req.IntentID = ensured.Intent.ID
	if _, err = repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationClose, req); err != nil {
		t.Fatal(err)
	}
	req.Token = uuid.NewString()
	req.Generation = ensured.Intent.Generation
	if _, err = pg.Exec(ctx, `
		UPDATE container_creation_intents
		SET
			inspection_token = $2,
			inspection_until = (clock_timestamp() AT TIME ZONE 'utc') + interval '45 seconds',
			inspection_errors = 2,
			inspection_error_code = 'Unavailable',
			inspection_error_message = 'original inspection failure'
		WHERE
			id = $1
	`, req.IntentID, req.Token); err != nil {
		t.Fatal(err)
	}
	req.Outcome = "INTERRUPTED"
	result, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationReport, req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Intent.InspectionErrors != 2 || result.Intent.Quarantined || result.Intent.Token != "" || result.Intent.InspectionErrorCode != "Unavailable" {
		t.Fatalf("shutdown spent budget or lost diagnostics: %+v", result.Intent)
	}
	// A late report cannot reuse the released inspection lease.
	req.Outcome = "ERROR"
	if _, err = repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationReport, req); status.Code(err) != codes.Aborted {
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
	ensured, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationEnsure, request)
	if err != nil {
		t.Fatal(err)
	}
	request.IntentID = ensured.Intent.ID
	grant, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationReserveCreate, request)
	if err != nil || !grant.Granted {
		t.Fatalf("create permission: %+v %v", grant, err)
	}
	// Simulate a granted Create whose daemon reply arrives after source deletion.
	if _, err = pg.Exec(ctx, "DELETE FROM workflows WHERE id=$1", workflow); err != nil {
		t.Fatal(err)
	}
	request.ContainerID = strings.Repeat("c", 64)
	observed, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationObserve, request)
	if err != nil {
		t.Fatal(err)
	}
	if !observed.Intent.Closed || observed.Intent.StartReserved {
		t.Fatalf("late observation regained authority: %+v", observed.Intent)
	}
	var route, instance, intent string
	err = pg.QueryRow(ctx, `
		SELECT
			payload -> 'ContainerCleanup' ->> 'RuntimeEndpoint',
			payload -> 'ContainerCleanup' ->> 'RuntimeInstance',
			payload -> 'ContainerCleanup' ->> 'IntentID'
		FROM
			outbox_events
		WHERE
			event_key = $1
	`, cleanupEventKey(job, request.ContainerID, request.RuntimeInstance)).Scan(&route, &instance, &intent)
	if err != nil || route != ensured.Intent.RuntimeEndpoint || instance != request.RuntimeInstance || intent != request.IntentID {
		t.Fatalf("late cleanup lost provenance: %q %q %q %v", route, instance, intent, err)
	}
	if _, err = repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationReserveStart, request); status.Code(err) != codes.Aborted {
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
	err = repo.RecordContainerCleanupReceipt(receiptCtx, jobsmodel.CleanupIdentity{
		JobID:           job,
		ContainerID:     id,
		RuntimeInstance: identity.RuntimeInstance,
		IntentID:        identity.IntentID,
	})
	cancel()
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("receipt bypassed the Start authority row lock: %v", err)
	}
	if err = lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	removed, err := repo.GetContainerCleanupReceipt(ctx, jobsmodel.CleanupIdentity{
		JobID:           job,
		ContainerID:     id,
		RuntimeInstance: identity.RuntimeInstance,
		IntentID:        identity.IntentID,
	})
	if err != nil || removed {
		t.Fatalf("blocked receipt committed: completed=%v error=%v", removed, err)
	}
	req := &jobsmodel.ContainerIntentCommand{
		JobID: job, LeaseToken: lease, Attempt: 1, IntentID: identity.IntentID,
		RuntimeInstance: identity.RuntimeInstance, ContainerID: id,
	}
	grant, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationReserveStart, req)
	if err != nil || !grant.Granted {
		t.Fatalf("Start after interrupted receipt: %+v %v", grant, err)
	}
	if err = repo.RecordContainerCleanupReceipt(ctx, jobsmodel.CleanupIdentity{
		JobID:           job,
		ContainerID:     id,
		RuntimeInstance: identity.RuntimeInstance,
		IntentID:        identity.IntentID,
	}); err != nil {
		t.Fatal(err)
	}
	var held int
	if err = pg.QueryRow(ctx, "SELECT count(*) FROM runtime_capacity_holds WHERE intent_id=$1 AND released_at IS NULL", identity.IntentID).Scan(&held); err != nil || held != 0 {
		t.Fatalf("receipt left capacity held: %d %v", held, err)
	}
}

//nolint:gocyclo // One persisted inspection generation proves quarantine, fencing and audited repair together.
func TestIntegrationContainerDiscoveryQuarantineAndAuditedRedrive(t *testing.T) {
	ctx := t.Context()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	user, workflow := seedUserWorkflow(ctx, t, pg)
	job, lease := seedRunningJob(ctx, t, pg, repo, workflow, user, t.Name())
	req := &jobsmodel.ContainerIntentCommand{
		JobID: job, LeaseToken: lease, Attempt: 1, WorkflowGeneration: 1, RuntimeInstance: "original-daemon", RequestHash: "frozen",
	}
	ensured, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationEnsure, req)
	if err != nil {
		t.Fatal(err)
	}
	req.IntentID = ensured.Intent.ID
	req.Outcome, req.ErrorCode, req.ErrorMessage = "UNKNOWN", "Unavailable", "original Create diagnostic"
	if _, err = repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationClose, req); err != nil {
		t.Fatal(err)
	}
	redrive := &jobsmodel.ContainerIntentCommand{IntentID: req.IntentID, Actor: "internal/operator", Reason: "original daemon restored", Generation: ensured.Intent.Generation}
	var staleToken string
	for attempt := 1; attempt <= 3; attempt++ {
		if _, err = pg.Exec(ctx, "UPDATE container_creation_intents SET next_inspection_at='1970-01-01' WHERE id=$1", req.IntentID); err != nil {
			t.Fatal(err)
		}
		batch, claimErr := repo.ListContainerIntents(ctx, &jobsmodel.ContainerIntentCommand{BatchSize: 1}, true)
		if claimErr != nil || len(batch) != 1 || batch[0].ID != req.IntentID {
			t.Fatalf("claim inspection: %+v %v", batch, claimErr)
		}
		if attempt == 1 {
			if _, err = repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationRedrive, redrive); status.Code(err) != codes.Aborted {
				t.Fatalf("live inspection was redriven: %v", err)
			}
		}
		staleToken = batch[0].Token
		result, reportErr := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationReport, &jobsmodel.ContainerIntentCommand{
			IntentID: req.IntentID, Token: staleToken, Generation: batch[0].Generation, Outcome: "ERROR", ErrorCode: "Unavailable", ErrorMessage: "inspection unreachable",
		})
		if reportErr != nil {
			t.Fatal(reportErr)
		}
		if result.Intent.InspectionErrors != int32(attempt) || result.Intent.Quarantined != (attempt == 3) || result.Intent.ErrorMessage != "original Create diagnostic" {
			t.Fatalf("inspection damaged durable outcome/budget: %+v", result.Intent)
		}
	}
	if _, err = pg.Exec(ctx, "UPDATE container_creation_intents SET next_inspection_at='1970-01-01' WHERE id=$1", req.IntentID); err != nil {
		t.Fatal(err)
	}
	batch, err := repo.ListContainerIntents(ctx, &jobsmodel.ContainerIntentCommand{BatchSize: 1}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) > 0 && batch[0].ID == req.IntentID {
		t.Fatal("quarantined intent was automatically reclaimed")
	}
	repaired, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationRedrive, redrive)
	if err != nil {
		t.Fatal(err)
	}
	if !repaired.Intent.Closed || repaired.Intent.Quarantined || repaired.Intent.Generation != ensured.Intent.Generation+1 || repaired.Intent.ErrorMessage != "original Create diagnostic" {
		t.Fatalf("redrive reopened or rewrote execution: %+v", repaired.Intent)
	}
	var actor, reason string
	var oldGeneration, newGeneration int64
	if err = pg.QueryRow(ctx, `
		SELECT
			actor,
			reason,
			expected_generation,
			generation
		FROM
			container_intent_redrives
		WHERE
			intent_id = $1
	`, req.IntentID).Scan(&actor, &reason, &oldGeneration, &newGeneration); err != nil {
		t.Fatal(err)
	}
	if actor != redrive.Actor || reason != redrive.Reason || oldGeneration != redrive.Generation || newGeneration != repaired.Intent.Generation {
		t.Fatalf("redrive lost audit identity: %q %q %d/%d", actor, reason, oldGeneration, newGeneration)
	}
	if _, err = repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationRedrive, redrive); status.Code(err) != codes.Aborted {
		t.Fatalf("stale redrive accepted: %v", err)
	}
	if _, err = repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationReport, &jobsmodel.ContainerIntentCommand{
		IntentID: req.IntentID, Token: staleToken, Generation: redrive.Generation, Outcome: "ABSENT",
	}); status.Code(err) != codes.Aborted {
		t.Fatalf("old-generation report accepted: %v", err)
	}
	grant, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationReserveCreate, req)
	if err != nil || grant.Granted {
		t.Fatalf("administrative redrive created execution authority: %+v %v", grant, err)
	}
	var audits int
	if err = pg.QueryRow(ctx, "SELECT count(*) FROM container_intent_redrives WHERE intent_id=$1", req.IntentID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("redrive replay duplicated audit: %d %v", audits, err)
	}
}

//nolint:gocyclo // One durable allocation scenario covers alternating and borrowed slots.
func TestIntegrationDiscoveryLanesAlternateAndBorrow(t *testing.T) {
	repo := isolatedContainerIntentRepository(t)
	ctx := t.Context()
	lanes := map[string]bool{}
	for n := range 6 {
		fixture := seedRetainedCleanupOn(t, repo.pg)
		urgent := n < 3
		lanes[fixture.IntentID] = urgent
		if _, err := repo.pg.Exec(ctx, `
			UPDATE container_creation_intents
			SET
				closed_at = now() AT TIME ZONE 'utc',
				inspection_urgent = $2
			WHERE
				id = $1
		`, fixture.IntentID, urgent); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := repo.ListContainerIntents(ctx, &jobsmodel.ContainerIntentCommand{}, false)
	require.NoError(t, err)
	require.Len(t, listed, 6, "admin listing remains independent of lane allocation")
	for _, size := range []int32{1, 3, 2} {
		var preferred bool
		if err = repo.pg.QueryRow(ctx, `SELECT next_urgent FROM container_discovery_turn`).Scan(&preferred); err != nil {
			t.Fatal(err)
		}
		claimed, claimErr := repo.ListContainerIntents(ctx, &jobsmodel.ContainerIntentCommand{BatchSize: size}, true)
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		if len(claimed) != int(size) {
			t.Fatalf("batch %d returned %d", size, len(claimed))
		}
		for i, intent := range claimed {
			if intent.Token == "" {
				t.Fatal("claim has no authority token")
			}
			if i < 2 && lanes[intent.ID] != (preferred == (i%2 == 0)) {
				t.Fatalf("interleaving: preferred=%v index=%d lane=%v", preferred, i, lanes[intent.ID])
			}
			delete(lanes, intent.ID)
		}
		var next bool
		if err = repo.pg.QueryRow(ctx, `SELECT next_urgent FROM container_discovery_turn`).Scan(&next); err != nil || next == preferred {
			t.Fatalf("turn did not alternate: next=%v err=%v", next, err)
		}
	}
	claimed, err := repo.ListContainerIntents(ctx, &jobsmodel.ContainerIntentCommand{BatchSize: 1}, true)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("live leases reclaimed: %v %v", claimed, err)
	}
	if _, err := repo.pg.Exec(ctx, `DELETE FROM container_discovery_turn`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ListContainerIntents(ctx, &jobsmodel.ContainerIntentCommand{}, true); status.Code(err) != codes.Internal {
		t.Fatalf("missing turn: %v", err)
	}
}

//nolint:gocyclo // Preserve the full new-evidence lifecycle at its database owner.
func TestIntegrationDiscoveryMarkerTracksNewEvidence(t *testing.T) {
	repo := isolatedContainerIntentRepository(t)
	ctx := t.Context()
	command := seedIntentFailureFixture(t, repo)
	command.Outcome = "SYSTEM"
	if _, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationClose, command); err != nil {
		t.Fatal(err)
	}
	readMarker := func() (bool, string) {
		t.Helper()
		var urgent bool
		var due string
		if err := repo.pg.QueryRow(ctx, `
			SELECT
				inspection_urgent,
				next_inspection_at::text
			FROM
				container_creation_intents
			WHERE
				id = $1
		`, command.IntentID).Scan(&urgent, &due); err != nil {
			t.Fatal(err)
		}
		return urgent, due
	}
	report := func(outcome string) {
		t.Helper()
		if _, err := repo.pg.Exec(ctx, `
			UPDATE container_creation_intents
			SET
				next_inspection_at = '1970-01-01'
			WHERE
				id = $1
		`, command.IntentID); err != nil {
			t.Fatal(err)
		}
		claimed, err := repo.ListContainerIntents(ctx, &jobsmodel.ContainerIntentCommand{BatchSize: 1}, true)
		if err != nil || len(claimed) != 1 {
			t.Fatalf("claim: %v %v", claimed, err)
		}
		req := *command
		req.Token, req.Generation, req.Outcome = claimed[0].Token, claimed[0].Generation, outcome
		if _, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationReport, &req); err != nil {
			t.Fatal(err)
		}
	}
	report(jobsmodel.ContainerInspectionAbsent)
	urgent, due := readMarker()
	if urgent {
		t.Fatal("healthy absent name did not enter surveillance")
	}
	for _, operation := range []string{jobsmodel.ContainerIntentOperationClose, jobsmodel.ContainerIntentOperationEnsure} {
		if _, err := repo.UpdateContainerIntent(ctx, operation, command); err != nil {
			t.Fatal(err)
		}
		if actual, at := readMarker(); actual || at != due {
			t.Fatalf("duplicate %s rearmed history: %v %s", operation, actual, at)
		}
	}
	if _, err := repo.pg.Exec(ctx, `SELECT close_job_container_intents($1)`, command.JobID); err != nil {
		t.Fatal(err)
	}
	if actual, at := readMarker(); actual || at != due {
		t.Fatal("SQL duplicate closure rearmed history")
	}
	command.ContainerID = strings.Repeat("a", 64)
	if _, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationObserve, command); err != nil {
		t.Fatal(err)
	}
	if actual, _ := readMarker(); !actual {
		t.Fatal("new late observation did not promote")
	}
	report(jobsmodel.ContainerInspectionObserved)
	if actual, _ := readMarker(); !actual {
		t.Fatal("unreceipted materialization downgraded")
	}
	if err := repo.RecordContainerCleanupReceipt(ctx, jobsmodel.CleanupIdentity{
		JobID: command.JobID, ContainerID: command.ContainerID,
		RuntimeInstance: command.RuntimeInstance, IntentID: command.IntentID,
	}); err != nil {
		t.Fatal(err)
	}
	report(jobsmodel.ContainerInspectionAbsent)
	urgent, due = readMarker()
	if urgent {
		t.Fatal("exact receipt did not permit downgrade")
	}
	if _, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationObserve, command); err != nil {
		t.Fatal(err)
	}
	if actual, at := readMarker(); actual || at != due {
		t.Fatal("duplicate observation rearmed history")
	}
	for _, outcome := range []string{jobsmodel.ContainerInspectionError, jobsmodel.ContainerInspectionInterrupted} {
		report(outcome)
		if actual, _ := readMarker(); actual {
			t.Fatalf("%s promoted historical errors", outcome)
		}
	}
	command.Generation, command.Actor, command.Reason = 1, "operator", "runtime repaired"
	if _, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationRedrive, command); err != nil {
		t.Fatal(err)
	}
	if actual, _ := readMarker(); !actual {
		t.Fatal("accepted repair did not promote")
	}
}

func TestIntegrationDiscoveryClosureClocksAndDuplicates(t *testing.T) {
	for _, owner := range []string{"Go", "job", "workflow"} {
		t.Run(owner, func(t *testing.T) {
			repo := isolatedContainerIntentRepository(t)
			command := seedIntentFailureFixture(t, repo)
			ctx := t.Context()
			_, err := repo.pg.Exec(ctx, `
				UPDATE container_creation_intents
				SET
					next_inspection_at = '2000-01-01'
				WHERE
					id = $1
			`, command.IntentID)
			require.NoError(t, err)
			closeIntent := func() {
				t.Helper()
				switch owner {
				case "Go":
					_, err = repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationClose, command)
				case "job":
					_, err = repo.pg.Exec(ctx, `SELECT close_job_container_intents($1)`, command.JobID)
				case "workflow":
					_, err = repo.pg.Exec(ctx, `
						UPDATE workflows
						SET
							terminated_at = now() AT TIME ZONE 'utc'
						WHERE
							id = (
								SELECT
									workflow_id
								FROM
									container_creation_intents
								WHERE
									id = $1
							)
					`, command.IntentID)
				}
				require.NoError(t, err)
			}
			closeIntent()
			var recent, urgent bool
			require.NoError(t, repo.pg.QueryRow(ctx, `
				SELECT
					next_inspection_at > now() AT TIME ZONE 'utc' - interval '1 minute',
					inspection_urgent
				FROM
					container_creation_intents
				WHERE
					id = $1
			`,
				command.IntentID).Scan(&recent, &urgent))
			require.True(t, recent, "first closure uses eligibility time, not creation time")
			require.True(t, urgent)
			_, err = repo.pg.Exec(ctx, `
				UPDATE container_creation_intents
				SET
					inspection_urgent = FALSE,
					next_inspection_at = '2099-01-01'
				WHERE
					id = $1
			`, command.IntentID)
			require.NoError(t, err)
			closeIntent()
			require.NoError(t, repo.pg.QueryRow(ctx, `
				SELECT
					next_inspection_at = '2099-01-01',
					inspection_urgent
				FROM
					container_creation_intents
				WHERE
					id = $1
			`, command.IntentID).Scan(&recent, &urgent))
			require.True(t, recent, "duplicate closure preserves rearm")
			require.False(t, urgent, "duplicate closure preserves surveillance")
		})
	}
}

func TestIntegrationDiscoveryReplicasAndLockedLane(t *testing.T) {
	repo := isolatedContainerIntentRepository(t)
	ctx := t.Context()
	fixtures := []retainedCleanupFixture{seedRetainedCleanupOn(t, repo.pg), seedRetainedCleanupOn(t, repo.pg)}
	for n, fixture := range fixtures {
		_, err := repo.pg.Exec(ctx, `
			UPDATE container_creation_intents
			SET
				closed_at = now() AT TIME ZONE 'utc',
				inspection_urgent = $2
			WHERE
				id = $1
		`, fixture.IntentID, n == 0)
		require.NoError(t, err)
	}
	tx, err := repo.pg.BeginTx(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck // Explicit rollback below releases the barrier.
	_, err = tx.Exec(ctx, `SELECT id FROM container_creation_intents WHERE id=$1 FOR UPDATE`, fixtures[0].IntentID)
	require.NoError(t, err)
	claimed, err := repo.ListContainerIntents(ctx, &jobsmodel.ContainerIntentCommand{BatchSize: 1}, true)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, fixtures[1].IntentID, claimed[0].ID, "busy urgent lane borrows surveillance")
	var urgent bool
	require.NoError(t, repo.pg.QueryRow(ctx, `SELECT next_urgent FROM container_discovery_turn`).Scan(&urgent))
	require.True(t, urgent, "actual borrowed lane determines next preference")
	require.NoError(t, tx.Rollback(ctx))
	_, err = repo.pg.Exec(ctx, `UPDATE container_creation_intents SET inspection_until=NULL,inspection_token=NULL`)
	require.NoError(t, err)
	type response struct {
		intents []*jobsmodel.ContainerIntent
		err     error
	}
	responses := make(chan response, 2)
	for range 2 {
		go func() {
			intents, claimErr := repo.ListContainerIntents(ctx, &jobsmodel.ContainerIntentCommand{BatchSize: 1}, true)
			responses <- response{intents, claimErr}
		}()
	}
	ids := map[string]bool{}
	for range 2 {
		result := <-responses
		require.NoError(t, result.err)
		require.Len(t, result.intents, 1)
		require.False(t, ids[result.intents[0].ID], "replicas issued overlapping live grants")
		ids[result.intents[0].ID] = true
	}
	require.Len(t, ids, 2)
}

func TestIntegrationDiscoveryReportAndObservationSerialize(t *testing.T) {
	for _, observationFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(observationFirst), func(t *testing.T) {
			repo := isolatedContainerIntentRepository(t)
			fixture := seedRetainedCleanupOn(t, repo.pg)
			monitor := testkit.Postgres(t)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			_, err := repo.pg.Exec(ctx, `
				UPDATE container_creation_intents
				SET
					closed_at = now() AT TIME ZONE 'utc'
				WHERE
					id = $1
			`, fixture.IntentID)
			require.NoError(t, err)
			require.NoError(t, repo.RecordContainerCleanupReceipt(ctx, jobsmodel.CleanupIdentity{
				JobID: fixture.JobID, ContainerID: fixture.ContainerID, RuntimeInstance: fixture.Instance, IntentID: fixture.IntentID,
			}))
			claimed, err := repo.ListContainerIntents(ctx, &jobsmodel.ContainerIntentCommand{BatchSize: 1}, true)
			require.NoError(t, err)
			require.Len(t, claimed, 1)
			report := &jobsmodel.ContainerIntentCommand{IntentID: fixture.IntentID, Token: claimed[0].Token, Generation: claimed[0].Generation, Outcome: jobsmodel.ContainerInspectionAbsent}
			observe := &jobsmodel.ContainerIntentCommand{IntentID: fixture.IntentID, RuntimeInstance: fixture.Instance, ContainerID: strings.Repeat("c", 64)}
			first, second := report, observe
			firstOp, secondOp := jobsmodel.ContainerIntentOperationReport, jobsmodel.ContainerIntentOperationObserve
			if observationFirst {
				first, second = second, first
				firstOp, secondOp = secondOp, firstOp
			}
			tx, err := repo.pg.BeginTx(ctx)
			require.NoError(t, err)
			defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck // Successful commit owns the normal path.
			var pid int
			require.NoError(t, tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid))
			_, err = repo.applyContainerIntent(ctx, tx, firstOp, first)
			require.NoError(t, err)
			done := make(chan error, 1)
			go func() { _, updateErr := repo.UpdateContainerIntent(ctx, secondOp, second); done <- updateErr }()
			// Observe an actual PostgreSQL lock wait before releasing the first writer.
			require.Eventually(t, func() bool {
				var waiting bool
				readErr := monitor.QueryRow(ctx, `
					SELECT
						EXISTS (
							SELECT
								1
							FROM
								pg_stat_activity
							WHERE
								$1 = ANY (pg_blocking_pids(pid))
						)
				`, pid).Scan(&waiting)
				return readErr == nil && waiting
			}, 5*time.Second, 5*time.Millisecond)
			require.NoError(t, tx.Commit(ctx))
			require.NoError(t, <-done)
			var urgent bool
			require.NoError(t, repo.pg.QueryRow(ctx, `SELECT inspection_urgent FROM container_creation_intents WHERE id=$1`, fixture.IntentID).Scan(&urgent))
			require.True(t, urgent, "neither lock order may hide newly observed unproven work")
		})
	}
}

func TestIntegrationDiscoveryDowngradeRequiresEveryPhysicalReceipt(t *testing.T) {
	repo := isolatedContainerIntentRepository(t)
	fixture := seedRetainedCleanupOn(t, repo.pg)
	ctx := t.Context()
	_, err := repo.pg.Exec(ctx, `
		UPDATE container_creation_intents
		SET
			closed_at = now() AT TIME ZONE 'utc'
		WHERE
			id = $1
	`, fixture.IntentID)
	require.NoError(t, err)
	_, err = repo.pg.Exec(ctx, `
		INSERT INTO
			runtime_capacity_holds (
				intent_id,
				job_id,
				attempt,
				container_id,
				runtime_node_id,
				runtime_instance
			)
		SELECT
			id,
			job_id,
			attempt,
			$2,
			runtime_node_id,
			runtime_instance
		FROM
			container_creation_intents
		WHERE
			id = $1
	`, fixture.IntentID, fixture.ContainerID)
	require.NoError(t, err)
	extra := strings.Repeat("d", 64)
	_, err = repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationObserve, &jobsmodel.ContainerIntentCommand{
		IntentID: fixture.IntentID, RuntimeInstance: fixture.Instance, ContainerID: extra,
	})
	require.NoError(t, err)
	for n, container := range []string{"", fixture.ContainerID, extra} {
		if container != "" {
			require.NoError(t, repo.RecordContainerCleanupReceipt(ctx, jobsmodel.CleanupIdentity{
				JobID: fixture.JobID, ContainerID: container, RuntimeInstance: fixture.Instance, IntentID: fixture.IntentID,
			}))
		}
		_, err = repo.pg.Exec(ctx, `
			UPDATE container_creation_intents
			SET
				next_inspection_at = '1970-01-01'
			WHERE
				id = $1
		`, fixture.IntentID)
		require.NoError(t, err)
		claimed, claimErr := repo.ListContainerIntents(ctx, &jobsmodel.ContainerIntentCommand{BatchSize: 1}, true)
		require.NoError(t, claimErr)
		require.Len(t, claimed, 1)
		_, err = repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationReport, &jobsmodel.ContainerIntentCommand{
			IntentID: fixture.IntentID, Token: claimed[0].Token, Generation: claimed[0].Generation, Outcome: jobsmodel.ContainerInspectionAbsent,
		})
		require.NoError(t, err)
		var urgent bool
		require.NoError(t, repo.pg.QueryRow(ctx, `SELECT inspection_urgent FROM container_creation_intents WHERE id=$1`, fixture.IntentID).Scan(&urgent))
		require.Equal(t, n < 2, urgent, "a receipt for only one materialization cannot retire the other obligation")
	}
}
