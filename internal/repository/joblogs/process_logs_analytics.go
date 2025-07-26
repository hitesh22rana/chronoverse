package joblogs

import (
	"context"
	"sync"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"

	analyticsmodel "github.com/hitesh22rana/chronoverse/internal/model/analytics"
	"github.com/hitesh22rana/chronoverse/internal/pkg/datastructures/countminsketch"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
)

// processLogsAnalytics processes accumulated logs analytics.
// It estimates the logs count for each workflow and produces analytics events to Kafka.
func (r *Repository) processLogsAnalytics(ctx context.Context, cms *countminsketch.CountMinSketch, uniqueWorkflows *sync.Map) error {
	ctx, span := r.tp.Start(ctx, "joblogs.Run.processLogsAnalytics")
	defer span.End()

	logger := loggerpkg.FromContext(ctx)

	totalUniqueWorkflows := 0
	uniqueWorkflows.Range(func(key, value any) bool {
		//nolint:errcheck // Ignore error as the key and value are expected to be of type string
		workflowID, _ := key.(string)

		//nolint:errcheck // Ignore error as the key and value are expected to be of type string
		userID, _ := value.(string)

		estimateLogsCount := cms.Estimate(workflowID)
		if estimateLogsCount == 0 {
			return true
		}

		totalUniqueWorkflows++

		analyticEventBytes, err := analyticsmodel.NewAnalyticEventBytes(
			userID,
			workflowID,
			analyticsmodel.EventTypeLogs,
			&analyticsmodel.EventTypeLogsData{
				Count: estimateLogsCount,
			},
		)
		if err != nil {
			return true
		}

		record := &kgo.Record{
			Topic: kafka.TopicAnalytics,
			Key:   []byte(workflowID),
			Value: analyticEventBytes,
		}

		// Asynchronously produce the record to Kafka
		r.kfk.Produce(context.WithoutCancel(ctx), record, func(_ *kgo.Record, _ error) {})

		return true
	})

	logger.Info(
		"successfully processed analytics",
		zap.Int("unique_workflows", totalUniqueWorkflows),
	)

	return nil
}
