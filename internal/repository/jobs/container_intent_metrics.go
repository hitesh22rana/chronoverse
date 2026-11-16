package jobs

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/metric"
)

// Metrics read durable state, so restarts and lost replies cannot reset the backlog.
// Instruments deliberately have no per-job, intent or runtime identity labels.
func (r *Repository) registerContainerIntentMetrics(meter metric.Meter) error {
	due, err := meter.Int64ObservableGauge("chronoverse.container_intents.discovery_due", metric.WithUnit("{intent}"))
	if err != nil {
		return err
	}
	quarantined, err := meter.Int64ObservableGauge("chronoverse.container_intents.quarantined", metric.WithUnit("{intent}"))
	if err != nil {
		return err
	}
	age, err := meter.Float64ObservableGauge("chronoverse.container_intents.oldest_due", metric.WithUnit("s"))
	if err != nil {
		return err
	}
	held, err := meter.Int64ObservableGauge("chronoverse.container_intents.capacity_holds", metric.WithUnit("{slot}"))
	if err != nil {
		return err
	}
	// The jobs repository and its callback share the service process lifetime.
	_, err = meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var dueCount, quarantineCount, heldCount int64
		var oldest float64
		queryErr := r.pg.QueryRow(ctx, `
SELECT
 count(*) FILTER (WHERE NOT quarantined AND next_inspection_at <= clock_timestamp() AT TIME ZONE 'utc'),
 count(*) FILTER (WHERE quarantined),
 GREATEST(0,COALESCE(max(EXTRACT(EPOCH FROM ((clock_timestamp() AT TIME ZONE 'utc')-next_inspection_at)))
   FILTER (WHERE NOT quarantined),0)),
 (SELECT count(*) FROM runtime_capacity_holds WHERE released_at IS NULL)
FROM container_creation_intents
WHERE closed_at IS NOT NULL`).Scan(&dueCount, &quarantineCount, &oldest, &heldCount)
		if queryErr != nil {
			return queryErr
		}
		observer.ObserveInt64(due, dueCount)
		observer.ObserveInt64(quarantined, quarantineCount)
		observer.ObserveFloat64(age, oldest)
		observer.ObserveInt64(held, heldCount)
		return nil
	}, due, quarantined, age, held)
	return err
}
