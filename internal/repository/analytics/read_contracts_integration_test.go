//nolint:testpackage // Integration tests use the repository constructor.
package analytics

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	analyticsmodel "github.com/hitesh22rana/chronoverse/internal/model/analytics"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestIntegrationAnalyticsReadIdentityErrors(t *testing.T) {
	pg := testkit.Postgres(t)
	repo := New(nil, pg)
	userID, workflowID := seedAnalytics(context.Background(), t, pg)
	for _, tc := range []struct {
		name, user, workflow string
		code                 codes.Code
	}{
		{"invalid user", "invalid", workflowID, codes.InvalidArgument},
		{"invalid workflow", userID, "invalid", codes.InvalidArgument},
		{"other user", uuid.NewString(), workflowID, codes.NotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repo.GetWorkflowAnalytics(context.Background(), tc.user, tc.workflow)
			if status.Code(err) != tc.code || got != nil {
				t.Fatalf("GetWorkflowAnalytics = %+v, %v; want nil, %v", got, err, tc.code)
			}
		})
	}
	if got, err := repo.GetUserAnalytics(context.Background(), "invalid"); status.Code(err) != codes.InvalidArgument || got != nil {
		t.Fatalf("GetUserAnalytics(invalid) = %+v, %v; want nil, InvalidArgument", got, err)
	}
	for _, tc := range []struct {
		name    string
		expired bool
		code    codes.Code
	}{{"canceled", false, codes.Canceled}, {"expired", true, codes.DeadlineExceeded}} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				ctx    context.Context
				cancel context.CancelFunc
			)
			if tc.expired {
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			cancel()
			if got, err := repo.GetUserAnalytics(ctx, userID); status.Code(err) != tc.code || got != nil {
				t.Fatalf("GetUserAnalytics = %+v, %v; want nil, %v", got, err, tc.code)
			}
			if got, err := repo.GetWorkflowAnalytics(ctx, userID, workflowID); status.Code(err) != tc.code || got != nil {
				t.Fatalf("GetWorkflowAnalytics = %+v, %v; want nil, %v", got, err, tc.code)
			}
		})
	}
}

func TestIntegrationAnalyticsReadTenantIsolation(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := New(nil, pg)
	userID, workflowID := seedAnalytics(ctx, t, pg)
	otherUser := testkit.SeedUser(ctx, t, pg, uuid.NewString()+"@chronoverse.test")
	if _, err := pg.Exec(ctx, `INSERT INTO analytics(user_id,workflow_id,kind,jobs_count,logs_count,total_job_execution_duration)
VALUES($1,$2,'HEARTBEAT',99,999,9999)`, otherUser, workflowID); err != nil {
		t.Fatal(err)
	}
	other, err := repo.GetUserAnalytics(ctx, otherUser)
	if err != nil {
		t.Fatal(err)
	}
	assertAnalyticsTotals(t, other, [4]uint64{1, 99, 999, 9999})
	if len(other.TopWorkflows) != 1 || other.TopWorkflows[0].WorkflowName != "Deleted workflow" {
		t.Fatalf("other tenant analytics = %+v", other)
	}
	owner, err := repo.GetUserAnalytics(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	assertAnalyticsTotals(t, owner, [4]uint64{2, 4, 14, 90})
	detail, err := repo.GetWorkflowAnalytics(ctx, otherUser, workflowID)
	if err != nil {
		t.Fatal(err)
	}
	if want := (analyticsmodel.GetWorkflowAnalyticsResponse{WorkflowID: workflowID, TotalJobs: 99, TotalJoblogs: 999, TotalJobExecutionDuration: 9999}); *detail != want {
		t.Fatalf("other tenant detail = %+v, %v", detail, err)
	}
	detail, err = repo.GetWorkflowAnalytics(ctx, userID, workflowID)
	if err != nil || detail == nil || detail.TotalJobs != 1 {
		t.Fatalf("owner workflow detail = %+v, %v", detail, err)
	}
}

func TestIntegrationAnalyticsReadDeletedWorkflow(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := New(nil, pg)
	userID, workflowID := seedAnalytics(ctx, t, pg)
	if _, err := pg.Exec(ctx, `DELETE FROM workflows WHERE id=$1 AND user_id=$2`, workflowID, userID); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetUserAnalytics(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	assertAnalyticsTotals(t, got, [4]uint64{2, 4, 14, 90})
	if len(got.WorkflowKinds) != 2 || len(got.TopWorkflows) != 2 {
		t.Fatalf("tenant breakdown = %+v", got)
	}
	found := false
	for _, row := range got.TopWorkflows {
		if row.WorkflowID == workflowID {
			found = true
			if row.WorkflowName != "Deleted workflow" || row.TotalJobs != 1 {
				t.Fatalf("deleted workflow = %+v", row)
			}
		}
	}
	if !found {
		t.Fatal("deleted workflow analytics missing")
	}

	detail, err := repo.GetWorkflowAnalytics(ctx, userID, workflowID)
	if err != nil || detail == nil || detail.TotalJobs != 1 {
		t.Fatalf("deleted workflow detail = %+v, %v", detail, err)
	}
}

func TestIntegrationAnalyticsReadTopLimitAndTieOrder(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := New(nil, pg)
	userID := testkit.SeedUser(ctx, t, pg, uuid.NewString()+"@chronoverse.test")
	if _, err := pg.Exec(ctx, `INSERT INTO analytics(user_id,workflow_id,kind,jobs_count,logs_count) VALUES($1,$2,'HEARTBEAT',0,100)`, userID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	zero, err := repo.GetUserAnalytics(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	assertAnalyticsTotals(t, zero, [4]uint64{1, 0, 100, 0})
	if len(zero.TopWorkflows) != 0 {
		t.Fatalf("zero-job workflow analytics = %+v", zero)
	}
	for n := 0; n < 12; n++ {
		id := fmt.Sprintf("00000000-0000-0000-0001-%012d", n)
		logs := 4
		if n == 10 {
			logs = 6
		}
		if _, err = pg.Exec(ctx, `INSERT INTO analytics(user_id,workflow_id,kind,jobs_count,logs_count,total_job_execution_duration) VALUES($1,$2,'CONTAINER',2,$3,6)`, userID, id, logs); err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.GetUserAnalytics(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	assertAnalyticsTotals(t, got, [4]uint64{13, 24, 150, 72})
	if len(got.TopWorkflows) != 10 {
		t.Fatalf("limited analytics = %+v", got)
	}
	wantKinds := []*analyticsmodel.WorkflowKindAnalytics{
		{Kind: "CONTAINER", TotalWorkflows: 12, TotalJobs: 24, TotalJoblogs: 50, TotalJobExecutionDuration: 72},
		{Kind: "HEARTBEAT", TotalWorkflows: 1, TotalJoblogs: 100},
	}
	if !reflect.DeepEqual(got.WorkflowKinds, wantKinds) {
		t.Fatalf("kind grouping = %+v, want %+v", got.WorkflowKinds, wantKinds)
	}
	for n, row := range got.TopWorkflows {
		id := n - 1
		if n == 0 {
			id = 10
		}
		if want := fmt.Sprintf("00000000-0000-0000-0001-%012d", id); row.WorkflowID != want || row.TotalJobs != 2 {
			t.Fatalf("top[%d] = %+v; want %s", n, row, want)
		}
	}
}

// analyticsReader gives permission failures their own connection pool.
func analyticsReader(t *testing.T, selectAnalytics bool) *Repository {
	t.Helper()
	ctx := context.Background()
	pg := testkit.Postgres(t)
	role := "analytics_" + uuid.New().String()[:8]
	if _, err := pg.Exec(ctx, `CREATE ROLE `+role+` LOGIN PASSWORD 'test-reader'`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pg.Exec(ctx, `DROP OWNED BY `+role); err != nil {
			t.Error(err)
		}
		if _, err := pg.Exec(ctx, `DROP ROLE `+role); err != nil {
			t.Error(err)
		}
	})
	if selectAnalytics {
		if _, err := pg.Exec(ctx, `GRANT SELECT ON analytics TO `+role); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := pgxpool.ParseConfig(testkit.PostgresDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := postgres.New(ctx, &postgres.Config{
		Host: cfg.ConnConfig.Host, Port: int(cfg.ConnConfig.Port), User: role, Password: "test-reader",
		Database: cfg.ConnConfig.Database, TLSConfig: &postgres.TLSConfig{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reader.Close)
	return New(nil, reader)
}

func TestIntegrationAnalyticsReadPermissionErrors(t *testing.T) {
	userID := uuid.NewString()
	workflowID := uuid.NewString()
	t.Run("analytics denied", func(t *testing.T) {
		repo := analyticsReader(t, false)
		if got, err := repo.GetUserAnalytics(context.Background(), userID); status.Code(err) != codes.Internal || got != nil {
			t.Fatalf("GetUserAnalytics = %+v, %v; want nil, Internal", got, err)
		}
		if got, err := repo.GetWorkflowAnalytics(context.Background(), userID, workflowID); status.Code(err) != codes.Internal || got != nil {
			t.Fatalf("GetWorkflowAnalytics = %+v, %v; want nil, Internal", got, err)
		}
	})
	t.Run("workflow names denied", func(t *testing.T) {
		repo := analyticsReader(t, true)
		if got, err := repo.GetUserAnalytics(context.Background(), userID); status.Code(err) != codes.Internal || got != nil {
			t.Fatalf("GetUserAnalytics = %+v, %v; want nil, Internal", got, err)
		}
	})
}

func TestIntegrationAnalyticsReadClosedPool(t *testing.T) {
	repo := analyticsReader(t, true)
	repo.pg.Close()
	if got, err := repo.GetUserAnalytics(context.Background(), uuid.NewString()); status.Code(err) != codes.Internal || got != nil {
		t.Fatalf("GetUserAnalytics(closed) = %+v, %v; want nil, Internal", got, err)
	}
	if got, err := repo.GetWorkflowAnalytics(context.Background(), uuid.NewString(), uuid.NewString()); status.Code(err) != codes.Internal || got != nil {
		t.Fatalf("GetWorkflowAnalytics(closed) = %+v, %v; want nil, Internal", got, err)
	}
}

func TestIntegrationAnalyticsReadNullableStoredKind(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := New(nil, pg)
	userID := testkit.SeedUser(ctx, t, pg, uuid.NewString()+"@chronoverse.test")
	workflowID := uuid.NewString()
	// The analytics schema permits a null kind; it reads as the empty kind.
	if _, err := pg.Exec(ctx, `INSERT INTO analytics(user_id,workflow_id,kind,jobs_count,logs_count,total_job_execution_duration)
VALUES($1,$2,NULL,1,2,3),($1,$3,'',4,5,6)`, userID, workflowID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetUserAnalytics(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	assertAnalyticsTotals(t, got, [4]uint64{2, 5, 7, 9})
	wantKinds := []*analyticsmodel.WorkflowKindAnalytics{{
		Kind: "", TotalWorkflows: 2, TotalJobs: 5, TotalJoblogs: 7, TotalJobExecutionDuration: 9,
	}}
	if !reflect.DeepEqual(got.WorkflowKinds, wantKinds) {
		t.Fatalf("null kind grouping = %+v", got.WorkflowKinds)
	}
	if len(got.TopWorkflows) != 2 || got.TopWorkflows[0].Kind != "" || got.TopWorkflows[1].Kind != "" {
		t.Fatalf("null kind top workflow = %+v", got.TopWorkflows)
	}
	if _, err = pg.Exec(ctx, `UPDATE analytics SET kind='HEARTBEAT' WHERE user_id=$1 AND workflow_id=$2`, userID, workflowID); err != nil {
		t.Fatal(err)
	}
	got, err = repo.GetUserAnalytics(ctx, userID)
	if err != nil || got == nil || len(got.WorkflowKinds) != 2 || got.WorkflowKinds[0].Kind != "" || got.WorkflowKinds[1].Kind != "HEARTBEAT" {
		t.Fatalf("GetUserAnalytics(repaired kind) = %+v, %v", got, err)
	}
}

func assertAnalyticsTotals(t *testing.T, got *analyticsmodel.GetUserAnalyticsResponse, want [4]uint64) {
	t.Helper()
	if got == nil {
		t.Fatal("analytics response is nil")
	}
	totals := [4]uint64{uint64(got.TotalWorkflows), got.TotalJobs, got.TotalJoblogs, got.TotalJobExecutionDuration}
	if totals != want {
		t.Fatalf("analytics totals = %v, want %v", totals, want)
	}
}

func TestIntegrationAnalyticsReadEmptyUser(t *testing.T) {
	pg := testkit.Postgres(t)
	got, err := New(nil, pg).GetUserAnalytics(t.Context(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	assertAnalyticsTotals(t, got, [4]uint64{})
	if len(got.WorkflowKinds) != 0 || len(got.TopWorkflows) != 0 {
		t.Fatalf("empty analytics breakdown = %+v", got)
	}
}
