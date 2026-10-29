//nolint:testpackage // Verifies repository behavior against PostgreSQL.
package jobs

import (
	"context"
	"encoding/base64"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

//nolint:gocyclo // Checks projections and ownership outcomes on one fixture.
func TestIntegrationJobReadOwnership(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository(t)
	userID, workflowID := seedUserWorkflow(ctx, t, repo.pg)
	jobID, err := repo.ScheduleJob(ctx, workflowID, userID, time.Now().UTC().Format(time.RFC3339Nano), "MANUAL", uuid.NewString(), 1)
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetJob(ctx, jobID, workflowID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != jobID || got.WorkflowID != workflowID || got.JobStatus != "PENDING" || got.JobTrigger != "MANUAL" || got.ScheduledAt.IsZero() || got.StartedAt.Valid || got.CompletedAt.Valid {
		t.Fatalf("unexpected job: %+v", got)
	}
	internal, err := repo.GetJobByID(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if internal.ID != jobID || internal.UserID != userID || internal.WorkflowID != workflowID || internal.JobStatus != got.JobStatus || !internal.ScheduledAt.Equal(got.ScheduledAt) {
		t.Fatalf("internal job differs: %+v, owned: %+v", internal, got)
	}
	for _, tc := range []struct {
		name, job, workflow, user string
		code                      codes.Code
	}{
		{"missing", uuid.NewString(), workflowID, userID, codes.NotFound},
		{"other user", jobID, workflowID, uuid.NewString(), codes.NotFound},
		{"other workflow", jobID, uuid.NewString(), userID, codes.NotFound},
		{"invalid job", "invalid", workflowID, userID, codes.InvalidArgument},
		{"invalid workflow", jobID, "invalid", userID, codes.InvalidArgument},
		{"invalid user", jobID, workflowID, "invalid", codes.InvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, readErr := repo.GetJob(ctx, tc.job, tc.workflow, tc.user)
			if result != nil || status.Code(readErr) != tc.code {
				t.Fatalf("result=%+v err=%v, want %v", result, readErr, tc.code)
			}
		})
	}
	for _, tc := range []struct {
		job  string
		code codes.Code
	}{{uuid.NewString(), codes.NotFound}, {"invalid", codes.InvalidArgument}} {
		result, readErr := repo.GetJobByID(ctx, tc.job)
		if result != nil || status.Code(readErr) != tc.code {
			t.Fatalf("GetJobByID(%q)=%+v err=%v, want %v", tc.job, result, readErr, tc.code)
		}
	}
}

func TestIntegrationJobReadFailures(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	userID, workflowID := seedUserWorkflow(ctx, t, repo.pg)
	jobID := uuid.NewString()
	cfg, err := pgxpool.ParseConfig(testkit.PostgresDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	unavailable, err := postgres.New(ctx, &postgres.Config{
		Host: cfg.ConnConfig.Host, Port: int(cfg.ConnConfig.Port), User: cfg.ConnConfig.User,
		Password: cfg.ConnConfig.Password, Database: cfg.ConnConfig.Database, TLSConfig: &postgres.TLSConfig{},
	})
	if err != nil {
		t.Fatal(err)
	}
	unavailable.Close()
	for _, tc := range []struct {
		name string
		pg   *postgres.Postgres
		code codes.Code
	}{
		{"canceled", repo.pg, codes.Canceled},
		{"deadline", repo.pg, codes.DeadlineExceeded},
		{"closed pool", unavailable, codes.Internal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller := ctx
			if tc.name == "canceled" {
				var cancel context.CancelFunc
				caller, cancel = context.WithCancel(ctx)
				cancel()
			}
			if tc.name == "deadline" {
				var cancel context.CancelFunc
				caller, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
			}
			reader := *repo
			reader.pg = tc.pg
			owned, readErr := reader.GetJob(caller, jobID, workflowID, userID)
			if owned != nil || status.Code(readErr) != tc.code {
				t.Fatalf("GetJob=%+v err=%v, want %v", owned, readErr, tc.code)
			}
			internal, readErr := reader.GetJobByID(caller, jobID)
			if internal != nil || status.Code(readErr) != tc.code {
				t.Fatalf("GetJobByID=%+v err=%v, want %v", internal, readErr, tc.code)
			}
			list, readErr := reader.ListJobs(caller, workflowID, userID, "", nil)
			if list != nil || status.Code(readErr) != tc.code {
				t.Fatalf("ListJobs=%+v err=%v, want %v", list, readErr, tc.code)
			}
		})
	}
}

func TestIntegrationListJobsInvalidRequests(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository(t)
	userID, workflowID := seedUserWorkflow(ctx, t, repo.pg)
	jobID := uuid.NewString()
	for _, tc := range []struct{ name, workflow, user, cursor string }{
		{"invalid workflow", "invalid", userID, ""},
		{"invalid user", workflowID, "invalid", ""},
		{"cursor parts", workflowID, userID, "invalid"},
		{"cursor timestamp", workflowID, userID, jobID + "$invalid"},
		{"cursor job", workflowID, userID, "invalid$" + time.Now().UTC().Format(time.RFC3339Nano)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, readErr := repo.ListJobs(ctx, tc.workflow, tc.user, tc.cursor, nil)
			if got != nil || status.Code(readErr) != codes.InvalidArgument {
				t.Fatalf("ListJobs=%+v err=%v, want InvalidArgument", got, readErr)
			}
		})
	}
}

//nolint:gocyclo // Checks exact page membership with both filter decoys.
func TestIntegrationListJobsCombinedFilters(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository(t)
	repo.cfg.FetchLimit = 2
	userID, workflowID := seedUserWorkflow(ctx, t, repo.pg)
	want := make([]string, 0, 3)
	for i := range 5 {
		trigger := "MANUAL"
		if i == 3 {
			trigger = "AUTOMATIC"
		}
		id, err := repo.ScheduleJob(ctx, workflowID, userID, time.Now().UTC().Format(time.RFC3339Nano), trigger, fmt.Sprintf("read-%s-%d", uuid.NewString(), i), 1)
		if err != nil {
			t.Fatal(err)
		}
		if i < 3 {
			want = append(want, id)
		} else if i == 4 {
			if _, err = repo.pg.Exec(ctx, `UPDATE jobs SET status='COMPLETED' WHERE id=$1`, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	filters := &jobsmodel.ListJobsFilters{Status: "PENDING", Trigger: "MANUAL"}
	first, err := repo.ListJobs(ctx, workflowID, userID, "", filters)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Jobs) != 2 || first.Cursor == "" {
		t.Fatalf("first page=%+v", first)
	}
	cursor, err := base64.StdEncoding.DecodeString(first.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.ListJobs(ctx, workflowID, userID, string(cursor), filters)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Jobs) != 1 || second.Cursor != "" {
		t.Fatalf("last page=%+v", second)
	}
	seen := make([]string, 0, 3)
	for _, page := range []*jobsmodel.ListJobsResponse{first, second} {
		for _, job := range page.Jobs {
			if job.JobStatus != "PENDING" || job.JobTrigger != "MANUAL" {
				t.Fatalf("unfiltered job=%+v", job)
			}
			seen = append(seen, job.ID)
		}
	}
	slices.Sort(seen)
	slices.Sort(want)
	if !slices.Equal(seen, want) {
		t.Fatalf("page identities=%v, want %v", seen, want)
	}
	for _, tc := range []struct{ workflow, user string }{{workflowID, uuid.NewString()}, {uuid.NewString(), userID}} {
		page, listErr := repo.ListJobs(ctx, tc.workflow, tc.user, "", nil)
		if listErr != nil || len(page.Jobs) != 0 || page.Cursor != "" {
			t.Fatalf("isolated page=%+v err=%v", page, listErr)
		}
	}
	automatic, err := repo.ListJobs(ctx, workflowID, userID, "", &jobsmodel.ListJobsFilters{Trigger: "AUTOMATIC"})
	if err != nil {
		t.Fatal(err)
	}
	if len(automatic.Jobs) != 1 || automatic.Jobs[0].JobTrigger != "AUTOMATIC" || automatic.Cursor != "" {
		t.Fatalf("automatic page=%+v", automatic)
	}
}
