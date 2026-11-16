package jobs

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/metric"
)

// Read committed backlog, not delivery counts: duplicates and restarts add no work.
// No job, container, runtime or actor labels are exported.
func (r *Repository) registerCleanupMetrics(meter metric.Meter) error {
	var outstanding, quarantined, dead metric.Int64ObservableGauge
	for _, gauge := range []struct {
		name   string
		target *metric.Int64ObservableGauge
	}{
		{"chronoverse.container_cleanup.outstanding", &outstanding},
		{"chronoverse.container_cleanup.quarantined", &quarantined},
		{"chronoverse.container_cleanup.publication_dead", &dead},
	} {
		instrument, instrumentErr := meter.Int64ObservableGauge(gauge.name, metric.WithUnit("{cleanup}"))
		if instrumentErr != nil {
			return instrumentErr
		}
		*gauge.target = instrument
	}
	age, err := meter.Float64ObservableGauge("chronoverse.container_cleanup.oldest_due", metric.WithUnit("s"))
	if err != nil {
		return err
	}
	// The callback shares the jobs repository's process lifetime.
	_, err = meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var count, quarantineCount, deadCount int64
		var oldest float64
		queryErr := r.pg.QueryRow(ctx, `
SELECT
 count(*),
 count(*) FILTER (WHERE cleanup_state='QUARANTINED'),
 count(*) FILTER (WHERE status='DEAD'),
 GREATEST(0,COALESCE(max(EXTRACT(EPOCH FROM (
  (clock_timestamp() AT TIME ZONE 'utc') - GREATEST(
   created_at,
   COALESCE(cleanup_next_attempt_at,created_at),
   COALESCE(cleanup_lease_until,created_at)
  )
 ))) FILTER (WHERE cleanup_state <> 'QUARANTINED'),0))
FROM outbox_events
WHERE topic='workflows'
 AND cleanup_state IS NOT NULL
 AND cleanup_state <> 'COMPLETED'`).Scan(&count, &quarantineCount, &deadCount, &oldest)
		if queryErr != nil {
			return queryErr
		}
		observer.ObserveInt64(outstanding, count)
		observer.ObserveInt64(quarantined, quarantineCount)
		observer.ObserveInt64(dead, deadCount)
		observer.ObserveFloat64(age, oldest)
		return nil
	}, outstanding, quarantined, dead, age)
	return err
}
