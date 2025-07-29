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

// processWorkflowsEvent processes workflows events and updates the analytics database.
func (r *Repository) processWorkflowsEvent(ctx context.Context, event *analyticsmodel.AnalyticEvent) error {
	logger := loggerpkg.FromContext(ctx)

	if event.Data == nil {
		return status.Error(codes.InvalidArgument, "missing event data for workflows event")
	}

	var data analyticsmodel.EventTypeWorkflowsData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return status.Error(codes.InvalidArgument, "invalid workflows event data format")
	}

	query := fmt.Sprintf(`
        INSERT INTO %s
        (user_id, workflow_id, kind)
        VALUES ($1, $2, $3)
        ON CONFLICT (user_id, workflow_id)
        DO UPDATE SET 
            kind = EXCLUDED.kind
    `, postgres.TableAnalytics)

	if _, err := r.pg.Exec(ctx, query, event.UserID, event.WorkflowID, data.Kind); err != nil {
		return status.Error(codes.Internal, "failed to insert/update workflows analytics")
	}

	logger.Info("successfully processed workflows event",
		zap.String("user_id", event.UserID),
		zap.String("workflow_id", event.WorkflowID),
		zap.String("kind", data.Kind),
	)

	return nil
}
