//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestMain(m *testing.M) {
	testkit.Run(m, testkit.WithPostgres())
}

var seedCounter int

// seedUserWorkflowJob inserts a fresh user, a completed workflow and a single
// PENDING job scheduled relative to now, returning the workflow and job ids.
func seedUserWorkflowJob(ctx context.Context, t *testing.T, pg *postgres.Postgres, scheduledIn time.Duration) (workflowID, jobID string) {
	t.Helper()

	seedCounter++
	userID := testkit.SeedUser(ctx, t, pg, fmt.Sprintf("%s-%d@chronoverse.test", t.Name(), seedCounter))
	workflowID = testkit.SeedWorkflow(ctx, t, pg, userID, t.Name()+"-workflow")

	if err := pg.QueryRow(ctx, `
		INSERT INTO jobs (workflow_id, user_id, status, scheduled_at)
		VALUES ($1, $2, 'PENDING', (now() AT TIME ZONE 'utc') + make_interval(secs => $3))
		RETURNING id
	`, workflowID, userID, scheduledIn.Seconds()).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	return workflowID, jobID
}

// insertJob adds another job to an existing workflow.
func insertJob(ctx context.Context, t *testing.T, pg *postgres.Postgres, workflowID, userID string, scheduledIn time.Duration) string {
	t.Helper()

	var jobID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO jobs (workflow_id, user_id, status, scheduled_at)
		VALUES ($1, $2, 'PENDING', (now() AT TIME ZONE 'utc') + make_interval(secs => $3))
		RETURNING id
	`, workflowID, userID, scheduledIn.Seconds()).Scan(&jobID); err != nil {
		t.Fatalf("insert job: %v", err)
	}
	return jobID
}

func TestIntegrationScheduleDueJobs(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := New(&Config{BatchSize: 10}, pg)

	_, jobID := seedUserWorkflowJob(ctx, t, pg, -time.Minute)

	total, err := repo.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if total != 1 {
		t.Fatalf("Run scheduled %d jobs, want 1", total)
	}

	// The job moved to QUEUED and got a dispatch attempt.
	var status string
	var dispatchAttempts int32
	if err := pg.QueryRow(ctx, `SELECT status, dispatch_attempts FROM jobs WHERE id = $1`, jobID).Scan(&status, &dispatchAttempts); err != nil {
		t.Fatalf("fetch job: %v", err)
	}
	if status != "QUEUED" {
		t.Fatalf("job status = %q, want %q", status, "QUEUED")
	}
	if dispatchAttempts != 1 {
		t.Fatalf("dispatch_attempts = %d, want 1", dispatchAttempts)
	}

	// A single outbox event with the job dispatch payload was written.
	var topic, eventKey string
	var payload []byte
	if err := pg.QueryRow(ctx, `
		SELECT topic, event_key, payload FROM outbox_events
		WHERE topic = $1 AND payload::text LIKE '%' || $2 || '%'
	`, kafka.TopicJobs, jobID).Scan(&topic, &eventKey, &payload); err != nil {
		t.Fatalf("fetch outbox event: %v", err)
	}
	if topic != kafka.TopicJobs {
		t.Fatalf("outbox topic = %q, want %q", topic, kafka.TopicJobs)
	}
	if eventKey == "" {
		t.Fatal("expected a non-empty outbox event_key")
	}

	// The payload is the job dispatch event the executor consumes (field names
	// are the Go field names, matching the model's round-trip encoding).
	var dispatched jobsmodel.ScheduledJobEntry
	if err := json.Unmarshal(payload, &dispatched); err != nil {
		t.Fatalf("unmarshal outbox payload: %v", err)
	}
	if dispatched.JobID != jobID {
		t.Fatalf("payload job_id = %q, want %q", dispatched.JobID, jobID)
	}
}

func TestIntegrationScheduleSkipsFutureJobs(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := New(&Config{BatchSize: 10}, pg)

	_, futureJobID := seedUserWorkflowJob(ctx, t, pg, time.Hour)

	total, err := repo.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if total != 0 {
		t.Fatalf("Run scheduled %d jobs, want 0", total)
	}

	var status string
	if err := pg.QueryRow(ctx, `SELECT status FROM jobs WHERE id = $1`, futureJobID).Scan(&status); err != nil {
		t.Fatalf("fetch future job: %v", err)
	}
	if status != "PENDING" {
		t.Fatalf("future job status = %q, want %q", status, "PENDING")
	}
}

func TestIntegrationScheduleSkipsWorkflowsWithActiveJobs(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := New(&Config{BatchSize: 10}, pg)

	// Workflow with a job that is already QUEUED must not dispatch its pending job.
	workflowID, activeJobID := seedUserWorkflowJob(ctx, t, pg, -time.Minute)
	if _, err := pg.Exec(ctx, `UPDATE jobs SET status = 'QUEUED' WHERE id = $1`, activeJobID); err != nil {
		t.Fatalf("mark job queued: %v", err)
	}
	blockedJobID := insertJob(ctx, t, pg, workflowID, mustUserID(ctx, t, pg, workflowID), -time.Minute)

	total, err := repo.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if total != 0 {
		t.Fatalf("Run scheduled %d jobs, want 0", total)
	}

	var status string
	if err := pg.QueryRow(ctx, `SELECT status FROM jobs WHERE id = $1`, blockedJobID).Scan(&status); err != nil {
		t.Fatalf("fetch blocked job: %v", err)
	}
	if status != "PENDING" {
		t.Fatalf("blocked job status = %q, want %q", status, "PENDING")
	}
}

// mustUserID returns the user_id of a workflow.
func mustUserID(ctx context.Context, t *testing.T, pg *postgres.Postgres, workflowID string) string {
	t.Helper()

	var userID string
	if err := pg.QueryRow(ctx, `SELECT user_id FROM workflows WHERE id = $1`, workflowID).Scan(&userID); err != nil {
		t.Fatalf("fetch workflow user: %v", err)
	}
	return userID
}
