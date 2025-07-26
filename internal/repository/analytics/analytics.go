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
			COALESCE(SUM(logs_count), 0) AS total_joblogs
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
			logs_count AS total_joblogs
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
