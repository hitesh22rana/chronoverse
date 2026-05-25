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

	if event.EventKey == "" {
		query := fmt.Sprintf(`
			INSERT INTO %s
			(user_id, workflow_id, jobs_count, total_job_execution_duration)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (user_id, workflow_id)
			DO UPDATE SET
				jobs_count = %s.jobs_count + EXCLUDED.jobs_count,
				total_job_execution_duration = %s.total_job_execution_duration + EXCLUDED.total_job_execution_duration
		`, postgres.TableAnalytics, postgres.TableAnalytics, postgres.TableAnalytics)

		if _, err := r.pg.Exec(ctx, query, event.UserID, event.WorkflowID, 1, data.JobExecutionDuration); err != nil {
			return status.Error(codes.Internal, "failed to insert/update jobs analytics")
		}
		logger.Info("successfully processed jobs event",
			zap.String("user_id", event.UserID),
			zap.String("workflow_id", event.WorkflowID),
			zap.Uint64("job_execution_duration", data.JobExecutionDuration),
		)
		return nil
	}

	query := fmt.Sprintf(`
		WITH processed AS (
			INSERT INTO %s (consumer, event_key)
			VALUES ($1, $2)
			ON CONFLICT DO NOTHING
			RETURNING event_key
		)
        INSERT INTO %s
        (user_id, workflow_id, jobs_count, total_job_execution_duration)
        SELECT $3, $4, $5, $6
		WHERE EXISTS (SELECT 1 FROM processed)
        ON CONFLICT (user_id, workflow_id)
        DO UPDATE SET
			jobs_count = %s.jobs_count + EXCLUDED.jobs_count,
            total_job_execution_duration = %s.total_job_execution_duration + EXCLUDED.total_job_execution_duration
    `, postgres.TableProcessedEvents, postgres.TableAnalytics, postgres.TableAnalytics, postgres.TableAnalytics)

	_, err := r.pg.Exec(ctx, query, "analytics-processor", event.EventKey, event.UserID, event.WorkflowID, 1, data.JobExecutionDuration)
	if err != nil {
		return status.Error(codes.Internal, "failed to insert/update jobs analytics")
	}

	logger.Info("successfully processed jobs event",
		zap.String("user_id", event.UserID),
		zap.String("workflow_id", event.WorkflowID),
		zap.Uint64("job_execution_duration", data.JobExecutionDuration),
	)

	return nil
}
