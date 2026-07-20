package analytics

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jackc/pgx/v5"

	analyticsmodel "github.com/hitesh22rana/chronoverse/internal/model/analytics"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const topWorkflowsLimit = 10

// Repository provides analytics repository.
type Repository struct {
	tp   trace.Tracer
	auth *auth.Auth
	pg   *postgres.Postgres
}

// New creates a new analytics repository.
func New(auth *auth.Auth, pg *postgres.Postgres) *Repository {
	return &Repository{
		tp:   otel.Tracer(svcpkg.Info().GetName()),
		auth: auth,
		pg:   pg,
	}
}

// GetUserAnalytics retrieves analytics data for a specific user.
func (r *Repository) GetUserAnalytics(ctx context.Context, userID string) (res *analyticsmodel.GetUserAnalyticsResponse, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.GetUserAnalytics")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Query to get user analytics including workflow count
	query := fmt.Sprintf(`
		SELECT
			COUNT(DISTINCT workflow_id) AS total_workflows,
			COALESCE(SUM(jobs_count), 0) AS total_jobs,
			COALESCE(SUM(logs_count), 0) AS total_joblogs,
			COALESCE(SUM(total_job_execution_duration), 0) AS total_job_execution_duration
		FROM %s
		WHERE user_id = $1
	`, postgres.TableAnalytics)

	rows, err := r.pg.Query(ctx, query, userID)
	if errors.Is(err, context.DeadlineExceeded) {
		err = status.Error(codes.DeadlineExceeded, err.Error())
		return nil, err
	} else if errors.Is(err, context.Canceled) {
		err = status.Error(codes.Canceled, err.Error())
		return nil, err
	}

	res, err = pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByName[analyticsmodel.GetUserAnalyticsResponse])
	if err != nil {
		if r.pg.IsNoRows(err) {
			err = status.Errorf(codes.NotFound, "no analytics found for user: %v", err)
			return nil, err
		} else if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid user ID: %v", err)
			return nil, err
		}

		err = status.Errorf(codes.Internal, "failed to get user analytics: %v", err)
		return nil, err
	}

	workflowKindsQuery := fmt.Sprintf(`
        SELECT
            kind,
            COUNT(*) AS total_workflows,
            COALESCE(SUM(jobs_count), 0) AS total_jobs,
            COALESCE(SUM(logs_count), 0) AS total_joblogs,
            COALESCE(SUM(total_job_execution_duration), 0) AS total_job_execution_duration
        FROM %s
        WHERE user_id = $1
        GROUP BY kind
        ORDER BY total_jobs DESC, kind ASC
    `, postgres.TableAnalytics)

	workflowKindRows, err := r.pg.Query(ctx, workflowKindsQuery, userID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, status.Error(codes.DeadlineExceeded, err.Error())
		} else if errors.Is(err, context.Canceled) {
			return nil, status.Error(codes.Canceled, err.Error())
		}

		return nil, status.Errorf(codes.Internal, "failed to get workflow kind analytics: %v", err)
	}

	res.WorkflowKinds, err = pgx.CollectRows(
		workflowKindRows,
		pgx.RowToAddrOfStructByName[analyticsmodel.WorkflowKindAnalytics],
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to collect workflow kind analytics: %v", err)
	}

	topWorkflowsQuery := fmt.Sprintf(`
        SELECT
            a.workflow_id,
            COALESCE(w.name, 'Deleted workflow') AS workflow_name,
            a.kind,
            a.jobs_count AS total_jobs,
            a.logs_count AS total_joblogs,
            a.total_job_execution_duration
        FROM %s a
        LEFT JOIN %s w ON w.id = a.workflow_id AND w.user_id = a.user_id
        WHERE a.user_id = $1 AND a.jobs_count > 0
        ORDER BY a.jobs_count DESC, a.logs_count DESC, a.workflow_id ASC
        LIMIT $2
    `, postgres.TableAnalytics, postgres.TableWorkflows)

	topWorkflowRows, err := r.pg.Query(ctx, topWorkflowsQuery, userID, topWorkflowsLimit)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, status.Error(codes.DeadlineExceeded, err.Error())
		} else if errors.Is(err, context.Canceled) {
			return nil, status.Error(codes.Canceled, err.Error())
		}

		return nil, status.Errorf(codes.Internal, "failed to get top workflow analytics: %v", err)
	}

	res.TopWorkflows, err = pgx.CollectRows(
		topWorkflowRows,
		pgx.RowToAddrOfStructByName[analyticsmodel.WorkflowAnalyticsSummary],
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to collect top workflow analytics: %v", err)
	}

	return res, nil
}

// GetWorkflowAnalytics retrieves analytics data for a specific workflow.
func (r *Repository) GetWorkflowAnalytics(ctx context.Context, userID, workflowID string) (res *analyticsmodel.GetWorkflowAnalyticsResponse, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.GetWorkflowAnalytics")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Query to get workflow analytics
	query := fmt.Sprintf(`
		SELECT
			workflow_id,
			jobs_count AS total_jobs,
			logs_count AS total_joblogs,
			total_job_execution_duration
		FROM %s
		WHERE user_id = $1 AND workflow_id = $2
		LIMIT 1
	`, postgres.TableAnalytics)

	rows, err := r.pg.Query(ctx, query, userID, workflowID)
	if errors.Is(err, context.DeadlineExceeded) {
		err = status.Error(codes.DeadlineExceeded, err.Error())
		return nil, err
	} else if errors.Is(err, context.Canceled) {
		err = status.Error(codes.Canceled, err.Error())
		return nil, err
	}

	res, err = pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByName[analyticsmodel.GetWorkflowAnalyticsResponse])
	if err != nil {
		if r.pg.IsNoRows(err) {
			err = status.Errorf(codes.NotFound, "no analytics found for workflow: %v", err)
			return nil, err
		} else if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid user ID or workflow ID: %v", err)
			return nil, err
		}

		err = status.Errorf(codes.Internal, "failed to get workflow analytics: %v", err)
		return nil, err
	}

	return res, nil
}
