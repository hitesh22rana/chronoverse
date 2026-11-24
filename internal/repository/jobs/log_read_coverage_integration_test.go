//nolint:testpackage // Exercises ownership and pagination against real storage.
package jobs

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestIntegrationStreamJobLogsRejections(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	userID, workflowID := seedUserWorkflow(ctx, t, pg)
	jobID, err := repo.ScheduleJob(ctx, workflowID, userID, time.Now().UTC().Format(time.RFC3339Nano), "MANUAL", "stream-guards-"+uuid.NewString(), 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, job, workflow, user string
		code                      codes.Code
	}{
		{"pending job", jobID, workflowID, userID, codes.FailedPrecondition},
		{"missing job", uuid.NewString(), workflowID, userID, codes.NotFound},
		{"other tenant", jobID, workflowID, uuid.NewString(), codes.NotFound},
		{"other workflow", jobID, uuid.NewString(), userID, codes.NotFound},
		{"malformed job", "invalid", workflowID, userID, codes.InvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sub, err := repo.StreamJobLogs(ctx, tc.job, tc.workflow, tc.user)
			if sub != nil || status.Code(err) != tc.code {
				t.Fatalf("subscription=%v error=%v, want %v", sub, err, tc.code)
			}
		})
	}
	for _, tc := range []struct {
		name    string
		expired bool
		code    codes.Code
	}{
		{name: "canceled", code: codes.Canceled},
		{name: "expired", expired: true, code: codes.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var caller context.Context
			var cancel context.CancelFunc
			if tc.expired {
				caller, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
			} else {
				caller, cancel = context.WithCancel(ctx)
			}
			cancel()

			sub, err := repo.StreamJobLogs(caller, jobID, workflowID, userID)
			if sub != nil || status.Code(err) != tc.code {
				t.Fatalf("subscription=%v error=%v, want %v", sub, err, tc.code)
			}
		})
	}
}

//nolint:gocyclo // Checks complete pages and job scope in the same fixture.
func TestIntegrationGetJobLogsOrderingAndScope(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	ch := testkit.ClickHouse(t)
	repo := newTestRepository(t)
	userID, workflowID := seedUserWorkflow(ctx, t, pg)
	jobID, err := repo.ScheduleJob(ctx, workflowID, userID, time.Now().UTC().Format(time.RFC3339Nano), "MANUAL", "read-order-"+uuid.NewString(), 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct {
		id     string
		seq    uint32
		stream string
	}{
		{"one", 1, "stdout"}, {"two-error", 2, "stderr"}, {"two-a", 2, "stdout"}, {"two-b", 2, "stdout"}, {"three", 3, "stdout"},
	} {
		if insertErr := ch.Exec(ctx,
			`
				INSERT INTO
					job_logs (
						event_id,
						user_id,
						workflow_id,
						job_id,
						timestamp,
						message,
						sequence_num,
						stream
					)
				VALUES
					(?, ?, ?, ?, ?, ?, ?, ?)
			`,
			jobID+f.id, userID, workflowID, jobID, time.Now().UTC(), f.id, f.seq, f.stream); insertErr != nil {
			t.Fatal(insertErr)
		}
	}
	for _, tc := range []struct {
		name   string
		order  jobsmodel.JobLogsSortOrder
		stream int
		want   []string
	}{
		{"ascending", jobsmodel.JobLogsSortOrderAsc, 0, []string{"one", "two-error", "two-a", "two-b", "three"}},
		{"descending", jobsmodel.JobLogsSortOrderDesc, 0, []string{"three", "two-error", "two-a", "two-b", "one"}},
		{"stderr", jobsmodel.JobLogsSortOrderAsc, 2, []string{"two-error"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			cursor := ""
			finished := false
			for page := 0; page < 6; page++ {
				result, err := repo.GetJobLogs(ctx, jobID, workflowID, userID, cursor, tc.order, &jobsmodel.GetJobLogsFilters{Stream: tc.stream})
				if err != nil {
					t.Fatalf("error=%v", err)
				}
				if len(result.JobLogs) == 0 || len(result.JobLogs) > repo.cfg.LogsFetchLimit {
					t.Fatalf("page size=%d, limit %d", len(result.JobLogs), repo.cfg.LogsFetchLimit)
				}
				for _, log := range result.JobLogs {
					got = append(got, log.Message)
				}
				if result.Cursor == "" {
					finished = true
					break
				}
				cursor = result.Cursor
			}
			if !finished {
				t.Fatal("pagination did not terminate")
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("logs=%v want %v", got, tc.want)
			}
		})
	}
	for _, tenant := range []string{userID, uuid.NewString()} {
		id := jobID
		if tenant == userID {
			id = uuid.NewString()
		}
		result, err := repo.GetJobLogs(ctx, id, workflowID, tenant, "", jobsmodel.JobLogsSortOrderAsc, &jobsmodel.GetJobLogsFilters{})
		if result != nil || status.Code(err) != codes.NotFound {
			t.Fatalf("ownership result=%v err=%v", result, err)
		}
	}
}

func TestIntegrationJobLogUnavailableClients(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository(t)
	pg := testkit.Postgres(t)
	userID, workflowID := seedUserWorkflow(ctx, t, pg)
	jobID, err := repo.ScheduleJob(ctx, workflowID, userID, time.Now().UTC().Format(time.RFC3339Nano), "MANUAL", "read-downstream-"+uuid.NewString(), 1)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(testkit.PostgresDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	isolated, err := postgres.New(ctx, &postgres.Config{
		Host: cfg.ConnConfig.Host, Port: int(cfg.ConnConfig.Port), User: cfg.ConnConfig.User,
		Password: cfg.ConnConfig.Password, Database: cfg.ConnConfig.Database, TLSConfig: &postgres.TLSConfig{},
	})
	if err != nil {
		t.Fatal(err)
	}
	isolated.Close()
	t.Run("postgres unavailable", func(t *testing.T) {
		repo.pg = isolated
		defer func() { repo.pg = pg }()
		sub, streamErr := repo.StreamJobLogs(ctx, jobID, workflowID, userID)
		if sub != nil || status.Code(streamErr) != codes.Internal {
			t.Fatalf("subscription=%v err=%v", sub, streamErr)
		}
	})
	isolatedCH, err := clickhouse.New(ctx, &clickhouse.Config{
		Hosts: []string{testkit.ClickHouseAddr(t)}, Database: "chronoverse", Username: "chronoverse-client",
		Password: "chronoverse", TLSConfig: &clickhouse.TLSConfig{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := isolatedCH.Close(); err != nil {
		t.Fatal(err)
	}
	originalCH := repo.ch
	t.Run("clickhouse unavailable", func(t *testing.T) {
		repo.ch = isolatedCH
		defer func() { repo.ch = originalCH }()

		result, err := repo.GetJobLogs(ctx, jobID, workflowID, userID, "", jobsmodel.JobLogsSortOrderAsc, &jobsmodel.GetJobLogsFilters{})
		if result != nil || status.Code(err) != codes.Internal {
			t.Fatalf("result=%v error=%v", result, err)
		}
	})
}

func TestIntegrationGetJobLogsContextFailures(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository(t)
	userID, workflowID := seedUserWorkflow(ctx, t, testkit.Postgres(t))
	jobID, err := repo.ScheduleJob(ctx, workflowID, userID, time.Now().UTC().Format(time.RFC3339Nano), "MANUAL", "read-context-"+uuid.NewString(), 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		expired bool
		code    codes.Code
	}{
		{"canceled", false, codes.Canceled}, {"expired", true, codes.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var caller context.Context
			var cancel context.CancelFunc
			if tc.expired {
				caller, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
			} else {
				caller, cancel = context.WithCancel(ctx)
			}
			cancel()

			result, err := repo.GetJobLogs(caller, jobID, workflowID, userID, "", jobsmodel.JobLogsSortOrderAsc, &jobsmodel.GetJobLogsFilters{})
			if result != nil || status.Code(err) != tc.code {
				t.Fatalf("result=%v error=%v want %v", result, err, tc.code)
			}
			// A rejected read leaves its clients usable for a subsequent request.
			result, err = repo.GetJobLogs(ctx, jobID, workflowID, userID, "", jobsmodel.JobLogsSortOrderAsc, &jobsmodel.GetJobLogsFilters{})
			if err != nil || result == nil || len(result.JobLogs) != 0 || result.Cursor != "" {
				t.Fatalf("retry result=%v error=%v", result, err)
			}
		})
	}
}
