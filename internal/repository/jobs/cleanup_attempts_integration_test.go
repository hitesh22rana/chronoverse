//nolint:testpackage // Exercises the durable repository with shared PostgreSQL fixtures.
package jobs

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

// retainedCleanupFixture is a standalone cleanup intent whose source job and
// workflow rows are intentionally absent: quarantine, redrive and completion
// must survive that deletion.
type retainedCleanupFixture struct {
	JobID       string
	Key         string
	ContainerID string
	Instance    string
	IntentID    string
}

func seedRetainedCleanup(t *testing.T) retainedCleanupFixture {
	t.Helper()
	return seedRetainedCleanupOn(t, testkit.Postgres(t))
}

func seedRetainedCleanupOn(t *testing.T, pg *postgres.Postgres) retainedCleanupFixture {
	t.Helper()
	nodeID := "retired-" + fixtureTag()
	fixture := retainedCleanupFixture{
		JobID:       uuid.NewString(),
		ContainerID: "old-container",
		Instance:    "daemon-" + fixtureTag(),
		IntentID:    uuid.NewString(),
	}
	fixture.Key = cleanupEventKey(fixture.JobID, fixture.ContainerID, fixture.Instance)
	if _, err := pg.Exec(t.Context(), `INSERT INTO container_creation_intents(`+
		`id,job_id,workflow_id,user_id,attempt,workflow_generation,process_instance_id,`+
		`runtime_node_id,runtime_endpoint,runtime_instance,request_hash,name,log_retention)
 VALUES($1,$2,$3,$4,1,1,$5,$6,'tcp://retired:2376',$7,$8,$9,false)`,
		fixture.IntentID, fixture.JobID, uuid.NewString(), uuid.NewString(), uuid.NewString(),
		nodeID, fixture.Instance, "request-"+fixtureTag(), "cv-"+fixtureTag()); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(t.Context(), `INSERT INTO container_materializations(intent_id,container_id,runtime_instance) VALUES($1,$2,$3)`,
		fixture.IntentID, fixture.ContainerID, fixture.Instance); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"EventKey": fixture.Key, "ID": uuid.NewString(), "UserID": uuid.NewString(), "JobID": fixture.JobID,
		"Action": "CONTAINER_CLEANUP", "CleanupGeneration": 1,
		"ContainerCleanup": map[string]any{
			"ContainerID": fixture.ContainerID, "RuntimeNodeID": nodeID,
			"RuntimeEndpoint": "tcp://retired:2376", "RuntimeInstance": fixture.Instance,
			"IntentID": fixture.IntentID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(t.Context(), `INSERT INTO outbox_events(topic,kafka_key,event_key,payload,status,published_at,cleanup_state)
 VALUES('workflows',$1,$2,$3,'PUBLISHED',now() AT TIME ZONE 'utc','READY')`,
		uuid.NewString(), fixture.Key, payload); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, statement := range []string{
			`DELETE FROM container_cleanup_receipts WHERE intent_id=$1`,
			`DELETE FROM runtime_capacity_holds WHERE intent_id=$1`,
			`DELETE FROM container_materializations WHERE intent_id=$1`,
			`DELETE FROM container_creation_intents WHERE id=$1`,
		} {
			if _, cleanupErr := pg.Exec(ctx, statement, fixture.IntentID); cleanupErr != nil {
				t.Error(cleanupErr)
			}
		}
		if _, err := pg.Exec(ctx, `DELETE FROM outbox_events WHERE event_key=$1;`, fixture.Key); err != nil {
			t.Error(err)
		}
	})
	return fixture
}

func TestIntegrationCleanupQuarantineRedriveSurvivesRestart(t *testing.T) {
	fixture := seedRetainedCleanup(t)
	repo := newTestRepository(t)
	update := func(op string, generation int64, token string, interrupted bool) *jobsmodel.CleanupAttempt {
		t.Helper()
		result, err := newTestRepository(t).UpdateContainerCleanup(t.Context(), op, jobsmodel.CleanupIdentity{
			JobID:           fixture.JobID,
			ContainerID:     fixture.ContainerID,
			RuntimeInstance: fixture.Instance,
			IntentID:        fixture.IntentID,
		}, generation, token, "Unavailable", interrupted, jobsmodel.CleanupRedrive{
			Actor:  "test/operator",
			Reason: "runtime restored",
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := update(jobsmodel.CleanupOperationBegin, 1, "", false)
	if first.State != "PROCESSING" || first.Attempts != 1 || first.Token == "" {
		t.Fatalf("bad authority: %+v", first)
	}
	for _, op := range []string{jobsmodel.CleanupOperationBegin, jobsmodel.CleanupOperationRedrive} {
		if _, err := repo.UpdateContainerCleanup(t.Context(), op, jobsmodel.CleanupIdentity{
			JobID:           fixture.JobID,
			ContainerID:     fixture.ContainerID,
			RuntimeInstance: fixture.Instance,
			IntentID:        fixture.IntentID,
		}, 1, "", "", false, jobsmodel.CleanupRedrive{
			Actor:  "test/operator",
			Reason: "runtime restored",
		}); status.Code(err) != codes.Aborted {
			t.Fatalf("active %s accepted: %v", op, err)
		}
	}
	if _, err := repo.UpdateContainerCleanup(t.Context(), jobsmodel.CleanupOperationFail, jobsmodel.CleanupIdentity{
		JobID:           fixture.JobID,
		ContainerID:     fixture.ContainerID,
		RuntimeInstance: fixture.Instance,
		IntentID:        fixture.IntentID,
	}, 1, uuid.NewString(), "Unavailable", false, jobsmodel.CleanupRedrive{
		Actor:  "test/operator",
		Reason: "runtime restored",
	}); status.Code(err) != codes.Aborted {
		t.Fatalf("wrong owner accepted: %v", err)
	}
	interrupted := update(jobsmodel.CleanupOperationFail, 1, first.Token, true)
	if interrupted.State != "READY" || interrupted.Attempts != 0 {
		t.Fatalf("cancellation spent budget: %+v", interrupted)
	}
	// A repeated response-lost report cannot refund twice.
	if again := update(jobsmodel.CleanupOperationFail, 1, first.Token, true); again.Attempts != 0 {
		t.Fatalf("duplicate refund: %+v", again)
	}
	exhaustCleanupGeneration(t, update, &fixture, first.Token)
	verifyCleanupRedrive(t, update, &fixture, first.Token)
}

type cleanupUpdate func(string, int64, string, bool) *jobsmodel.CleanupAttempt

func exhaustCleanupGeneration(t *testing.T, update cleanupUpdate, fixture *retainedCleanupFixture, oldToken string) {
	t.Helper()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	for attempt := int32(1); attempt <= 3; attempt++ {
		current := update(jobsmodel.CleanupOperationBegin, 1, "", false)
		if current.Attempts != attempt || current.Token == oldToken {
			t.Fatalf("attempt=%+v", current)
		}
		failed := update(jobsmodel.CleanupOperationFail, 1, current.Token, false)
		want := "RETRY"
		if attempt == 3 {
			want = "QUARANTINED"
		}
		if failed.State != want {
			t.Fatalf("failure=%+v", failed)
		}
		if again := update(jobsmodel.CleanupOperationFail, 1, current.Token, false); again.State != want || again.Attempts != attempt {
			t.Fatalf("duplicate failure=%+v", again)
		}
		if attempt < 3 {
			if _, err := repo.UpdateContainerCleanup(t.Context(), jobsmodel.CleanupOperationBegin, jobsmodel.CleanupIdentity{
				JobID:           fixture.JobID,
				ContainerID:     fixture.ContainerID,
				RuntimeInstance: fixture.Instance,
				IntentID:        fixture.IntentID,
			}, 1, "", "", false, jobsmodel.CleanupRedrive{
				Actor:  "test/operator",
				Reason: "runtime restored",
			}); status.Code(err) != codes.Aborted {
				t.Fatalf("backoff bypassed: %v", err)
			}
			var seconds float64
			if err := pg.QueryRow(t.Context(), `SELECT EXTRACT(EPOCH FROM cleanup_next_attempt_at-cleanup_failed_at) FROM outbox_events WHERE event_key=$1`, fixture.Key).Scan(&seconds); err != nil {
				t.Fatal(err)
			}
			wait := time.Duration(seconds) * time.Second
			if wait != time.Duration(attempt)*30*time.Second {
				t.Fatalf("retry delay=%v", wait)
			}
			if _, err := pg.Exec(t.Context(), `UPDATE outbox_events SET cleanup_next_attempt_at=now() AT TIME ZONE 'utc'-interval '1 second' WHERE event_key=$1`, fixture.Key); err != nil {
				t.Fatal(err)
			}
		}
	}
	if q := update(jobsmodel.CleanupOperationBegin, 1, "", false); q.State != "QUARANTINED" || q.Token != "" {
		t.Fatalf("quarantine granted authority: %+v", q)
	}
}

func verifyCleanupRedrive(t *testing.T, update cleanupUpdate, fixture *retainedCleanupFixture, oldToken string) {
	t.Helper()
	pg := testkit.Postgres(t)
	redrive := update(jobsmodel.CleanupOperationRedrive, 1, "", false)
	if redrive.Generation != 2 || redrive.Attempts != 0 || redrive.State != "READY" {
		t.Fatalf("redrive=%+v", redrive)
	}
	var state string
	var generation int64
	if err := pg.QueryRow(t.Context(), `SELECT status,(payload->>'CleanupGeneration')::bigint FROM outbox_events WHERE event_key=$1`, fixture.Key).Scan(&state, &generation); err != nil {
		t.Fatal(err)
	}
	if state != "PENDING" || generation != 2 {
		t.Fatalf("not requeued: %s/%d", state, generation)
	}
	for _, op := range []string{jobsmodel.CleanupOperationBegin, jobsmodel.CleanupOperationFail, jobsmodel.CleanupOperationRedrive} {
		if stale := update(op, 1, oldToken, false); stale.State != "STALE" || stale.Generation != 2 {
			t.Fatalf("old %s revived generation: %+v", op, stale)
		}
	}
	current := update(jobsmodel.CleanupOperationBegin, 2, "", false)
	if current.Attempts != 1 {
		t.Fatalf("new budget=%+v", current)
	}
	verifyCleanupCompletion(t, update, fixture, current.Token)
}

func verifyCleanupCompletion(t *testing.T, update cleanupUpdate, fixture *retainedCleanupFixture, currentToken string) {
	t.Helper()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	var state string
	if err := repo.RecordContainerCleanupReceipt(t.Context(), jobsmodel.CleanupIdentity{
		JobID:           fixture.JobID,
		ContainerID:     fixture.ContainerID,
		RuntimeInstance: fixture.Instance,
		IntentID:        fixture.IntentID,
	}); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{jobsmodel.CleanupOperationBegin, jobsmodel.CleanupOperationFail, jobsmodel.CleanupOperationRedrive} {
		if completed := update(op, 2, currentToken, false); completed.State != "COMPLETED" {
			t.Fatalf("receipt ignored: %+v", completed)
		}
	}
	var token *string
	if err := pg.QueryRow(t.Context(), `SELECT cleanup_state,cleanup_token::text FROM outbox_events WHERE event_key=$1`, fixture.Key).Scan(&state, &token); err != nil {
		t.Fatal(err)
	}
	if state != "COMPLETED" || token != nil {
		t.Fatalf("receipt didn't retire authority: %s/%v", state, token)
	}
	if _, err := pg.Exec(t.Context(), `DELETE FROM outbox_events WHERE event_key=$1`, fixture.Key); err != nil {
		t.Fatal(err)
	}
	if result := update(jobsmodel.CleanupOperationBegin, 2, "", false); result.State != "COMPLETED" {
		t.Fatal("pruned intent ignored receipt")
	}
}

func TestIntegrationCleanupAbandonedAttemptBudget(t *testing.T) {
	fixture := seedRetainedCleanup(t)
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	var oldToken string
	for attempt := int32(1); attempt <= 4; attempt++ {
		result, err := repo.UpdateContainerCleanup(t.Context(), jobsmodel.CleanupOperationBegin, jobsmodel.CleanupIdentity{
			JobID:           fixture.JobID,
			ContainerID:     fixture.ContainerID,
			RuntimeInstance: fixture.Instance,
			IntentID:        fixture.IntentID,
		}, 1, "", "", false, jobsmodel.CleanupRedrive{
			Actor:  "test/operator",
			Reason: "runtime restored",
		})
		if err != nil {
			t.Fatal(err)
		}
		if attempt == 4 {
			if result.State != "QUARANTINED" || result.Attempts != 3 {
				t.Fatalf("unbounded abandoned work: %+v", result)
			}
			break
		}
		if result.State != "PROCESSING" || result.Attempts != attempt {
			t.Fatalf("authority=%+v", result)
		}
		if oldToken != "" {
			if _, err := repo.UpdateContainerCleanup(t.Context(), jobsmodel.CleanupOperationFail, jobsmodel.CleanupIdentity{
				JobID:           fixture.JobID,
				ContainerID:     fixture.ContainerID,
				RuntimeInstance: fixture.Instance,
				IntentID:        fixture.IntentID,
			}, 1, oldToken, "Unavailable", false, jobsmodel.CleanupRedrive{
				Actor:  "test/operator",
				Reason: "runtime restored",
			}); status.Code(err) != codes.Aborted {
				t.Fatalf("expired owner changed successor: %v", err)
			}
		}
		oldToken = result.Token
		if _, err := pg.Exec(t.Context(), `UPDATE outbox_events SET cleanup_lease_until=now() AT TIME ZONE 'utc'-interval '1 second' WHERE event_key=$1`, fixture.Key); err != nil {
			t.Fatal(err)
		}
		repo = newTestRepository(t)
	}
	if _, err := repo.UpdateContainerCleanup(t.Context(), jobsmodel.CleanupOperationBegin, jobsmodel.CleanupIdentity{
		JobID:           uuid.NewString(),
		ContainerID:     "absent",
		RuntimeInstance: "daemon-" + fixtureTag(),
		IntentID:        uuid.NewString(),
	}, 1, "", "", false, jobsmodel.CleanupRedrive{
		Actor:  "test/operator",
		Reason: "runtime restored",
	}); status.Code(err) != codes.Unavailable {
		t.Fatalf("missing intent accepted: %v", err)
	}
	fresh := seedRetainedCleanup(t)
	if _, err := repo.UpdateContainerCleanup(t.Context(), "unknown", jobsmodel.CleanupIdentity{
		JobID:           fresh.JobID,
		ContainerID:     fresh.ContainerID,
		RuntimeInstance: fresh.Instance,
		IntentID:        fresh.IntentID,
	}, 1, "", "", false, jobsmodel.CleanupRedrive{
		Actor:  "test/operator",
		Reason: "runtime restored",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("unsupported transition accepted: %v", err)
	}
}

func TestIntegrationCleanupStorageFailuresDoNotGrantAuthority(t *testing.T) {
	for _, phase := range []string{"canceled", "read intent", "read receipt", "write", "commit"} {
		t.Run(phase, func(t *testing.T) {
			fixture := seedRetainedCleanup(t)
			repo := newTestRepository(t)
			pg := testkit.Postgres(t)
			ctx, want := cleanupStorageFailureContext(t, phase)
			result, err := repo.UpdateContainerCleanup(ctx, jobsmodel.CleanupOperationBegin, jobsmodel.CleanupIdentity{
				JobID:           fixture.JobID,
				ContainerID:     fixture.ContainerID,
				RuntimeInstance: fixture.Instance,
				IntentID:        fixture.IntentID,
			}, 1, "", "", false, jobsmodel.CleanupRedrive{
				Actor:  "test/operator",
				Reason: "runtime restored",
			})
			if result != nil || status.Code(err) != want {
				t.Fatalf("%s granted authority: %+v / %v", phase, result, err)
			}
			// Read with the failing caller detached; no acquisition may survive rollback.
			if phase != "read intent" && phase != "read receipt" {
				var state string
				var attempts int
				if err := pg.QueryRow(t.Context(), `SELECT cleanup_state,cleanup_attempts FROM outbox_events WHERE event_key=$1`, fixture.Key).Scan(&state, &attempts); err != nil {
					t.Fatal(err)
				}
				if state != "READY" || attempts != 0 {
					t.Fatalf("failed storage spent authority: %s/%d", state, attempts)
				}
			}
		})
	}
}

func cleanupStorageFailureContext(t *testing.T, phase string) (context.Context, codes.Code) {
	t.Helper()
	pg := testkit.Postgres(t)
	ctx := t.Context()
	want := codes.Internal
	switch phase {
	case "canceled":
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		ctx = canceled
		want = codes.Canceled
	case "read intent", "read receipt":
		table := "outbox_events"
		if phase == "read receipt" {
			table = "container_cleanup_receipts"
		}
		tx, err := pg.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, "LOCK TABLE "+table+" IN ACCESS EXCLUSIVE MODE"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := tx.Rollback(context.WithoutCancel(ctx)); err != nil {
				t.Error(err)
			}
		})
		timed, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		t.Cleanup(cancel)
		ctx = timed
		want = codes.DeadlineExceeded
	case "write", "commit":
		timing := "BEFORE UPDATE ON outbox_events"
		constraint := ""
		if phase == "commit" {
			timing = "AFTER UPDATE ON outbox_events DEFERRABLE INITIALLY DEFERRED"
			constraint = "CONSTRAINT "
		}
		ddl := `CREATE FUNCTION reject_cleanup_test() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN RAISE EXCEPTION 'injected storage failure'; END; $$; CREATE ` + constraint + `TRIGGER reject_cleanup_test ` + timing +
			` FOR EACH ROW EXECUTE FUNCTION reject_cleanup_test();`
		if _, err := pg.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := pg.Exec(context.WithoutCancel(ctx), `DROP TRIGGER reject_cleanup_test ON outbox_events; DROP FUNCTION reject_cleanup_test();`); err != nil {
				t.Error(err)
			}
		})
	}
	return ctx, want
}

func TestIntegrationCleanupConcurrentDeliveryAndRedrive(t *testing.T) {
	for _, operation := range []string{jobsmodel.CleanupOperationBegin, jobsmodel.CleanupOperationRedrive} {
		t.Run(operation, func(t *testing.T) {
			fixture := seedRetainedCleanup(t)
			repo := newTestRepository(t)
			type outcome struct {
				attempt *jobsmodel.CleanupAttempt
				err     error
			}
			results := make(chan outcome, 2)
			start := make(chan struct{})
			for range 2 {
				go func() {
					<-start
					result, err := repo.UpdateContainerCleanup(t.Context(), operation, jobsmodel.CleanupIdentity{
						JobID:           fixture.JobID,
						ContainerID:     fixture.ContainerID,
						RuntimeInstance: fixture.Instance,
						IntentID:        fixture.IntentID,
					}, 1, "", "", false, jobsmodel.CleanupRedrive{
						Actor:  "test/operator",
						Reason: "runtime restored",
					})
					results <- outcome{result, err}
				}()
			}
			close(start)
			states := map[string]int{}
			for range 2 {
				result := <-results
				if result.err != nil {
					states[status.Code(result.err).String()]++
					continue
				}
				states[result.attempt.State]++
			}
			if operation == jobsmodel.CleanupOperationBegin {
				if states["PROCESSING"] != 1 || states["Aborted"] != 1 {
					t.Fatalf("two cleanup owners: %v", states)
				}
			} else {
				if states["READY"] != 1 || states["STALE"] != 1 {
					t.Fatalf("duplicate redrive reset budget: %v", states)
				}
				var count int
				if err := testkit.Postgres(t).QueryRow(t.Context(), "SELECT count(*) FROM container_cleanup_redrives WHERE event_key=$1", fixture.Key).Scan(&count); err != nil || count != 1 {
					t.Fatalf("concurrent redrive audit count=%d error=%v", count, err)
				}
			}
		})
	}
}
