//nolint:dupl // Ignore dupl check as it's a common pattern for processing events
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

// processLogsEvent processes logs events and updates the analytics database.
func (r *Repository) processLogsEvent(ctx context.Context, event *analyticsmodel.AnalyticEvent) error {
	logger := loggerpkg.FromContext(ctx)

	if event.Data == nil {
		logger.Error("missing event data for logs event")
		return status.Error(codes.InvalidArgument, "missing event data for logs event")
	}

	// Unmarshal json.RawMessage to the proper struct
	var data analyticsmodel.EventTypeLogsData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		logger.Error("failed to unmarshal logs event data",
			zap.Error(err),
			zap.Any("event", event),
		)
		return status.Error(codes.InvalidArgument, "invalid logs event data format")
	}

	query := fmt.Sprintf(`
        INSERT INTO %s
        (user_id, workflow_id, logs_count)
        VALUES ($1, $2, $3)
        ON CONFLICT (user_id, workflow_id)
        DO UPDATE SET 
            logs_count = %s.logs_count + EXCLUDED.logs_count
    `, postgres.TableAnalytics, postgres.TableAnalytics)

	_, err := r.pg.Exec(ctx, query, event.UserID, event.WorkflowID, data.Count)
	if err != nil {
		return status.Error(codes.Internal, "failed to insert/update logs analytics")
	}

	logger.Info("successfully processed logs event",
		zap.String("user_id", event.UserID),
		zap.String("workflow_id", event.WorkflowID),
		zap.Uint64("logs_count", data.Count),
	)

	return nil
}
