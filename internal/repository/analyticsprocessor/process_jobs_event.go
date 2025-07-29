package analyticsprocessor

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	analyticsmodel "github.com/hitesh22rana/chronoverse/internal/model/analytics"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

// processJobsEvent processes jobs events and updates the analytics database.
func (r *Repository) processJobsEvent(ctx context.Context, event *analyticsmodel.AnalyticEvent) error {
	logger := loggerpkg.FromContext(ctx)

	if event.Data == nil {
		return status.Error(codes.InvalidArgument, "missing event data for jobs event")
	}

	// Unmarshal json.RawMessage to the proper struct
	var data analyticsmodel.EventTypeJobsData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return status.Error(codes.InvalidArgument, "invalid jobs event data format")
	}

	query := fmt.Sprintf(`
        INSERT INTO %s
        (user_id, workflow_id, jobs_count, avg_job_execution_duration_ms)
        VALUES ($1, $2, $3, $4)
        ON CONFLICT (user_id, workflow_id)
        DO UPDATE SET 
            jobs_count = %s.jobs_count + EXCLUDED.jobs_count,
            avg_job_execution_duration_ms = (%s.avg_job_execution_duration_ms + EXCLUDED.avg_job_execution_duration_ms) / 2
    `, postgres.TableAnalytics, postgres.TableAnalytics, postgres.TableAnalytics)

	_, err := r.pg.Exec(ctx, query, event.UserID, event.WorkflowID, 1, data.JobExecutionDurationMs)
	if err != nil {
		return status.Error(codes.Internal, "failed to insert/update jobs analytics")
	}

	logger.Info("successfully processed jobs event",
		zap.String("user_id", event.UserID),
		zap.String("workflow_id", event.WorkflowID),
		zap.Uint64("job_execution_duration_ms", data.JobExecutionDurationMs),
	)

	return nil
}
