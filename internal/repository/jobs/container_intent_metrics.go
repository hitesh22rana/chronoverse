package jobs

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
)

// containerIntentLaneLabel is the only dimension on discovery scheduling series.
const containerIntentLaneLabel = "lane"

// Metrics read durable state, so restarts and lost replies cannot reset the backlog.
// Due and age series carry a bounded scheduling-lane label and no duplicate totals;
// quarantine and held capacity stay global.
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
		var urgentDue, surveillanceDue, quarantineCount, heldCount int64
		var urgentAge, surveillanceAge float64
		queryErr := r.pg.QueryRow(ctx, `
			SELECT
				count(*) FILTER (
					WHERE
						NOT quarantined
						AND inspection_urgent
						AND next_inspection_at <= clock_timestamp() AT TIME ZONE 'utc'
				),
				count(*) FILTER (
					WHERE
						NOT quarantined
						AND NOT inspection_urgent
						AND next_inspection_at <= clock_timestamp() AT TIME ZONE 'utc'
				),
				GREATEST(
					0,
					COALESCE(
						max(
							EXTRACT(
								EPOCH
								FROM
									(
										(clock_timestamp() AT TIME ZONE 'utc') - next_inspection_at
									)
							)
						) FILTER (
							WHERE
								NOT quarantined
								AND inspection_urgent
						),
						0
					)
				),
				GREATEST(
					0,
					COALESCE(
						max(
							EXTRACT(
								EPOCH
								FROM
									(
										(clock_timestamp() AT TIME ZONE 'utc') - next_inspection_at
									)
							)
						) FILTER (
							WHERE
								NOT quarantined
								AND NOT inspection_urgent
						),
						0
					)
				),
				count(*) FILTER (
					WHERE
						quarantined
				),
				(
					SELECT
						count(*)
					FROM
						runtime_capacity_holds
					WHERE
						released_at IS NULL
				)
			FROM
				container_creation_intents
			WHERE
				closed_at IS NOT NULL
		`).
			Scan(&urgentDue, &surveillanceDue, &urgentAge, &surveillanceAge, &quarantineCount, &heldCount)
		if queryErr != nil {
			return queryErr
		}
		urgent := metric.WithAttributes(attribute.String(containerIntentLaneLabel, jobsmodel.ContainerInspectionLaneUrgent))
		surveillance := metric.WithAttributes(attribute.String(containerIntentLaneLabel, jobsmodel.ContainerInspectionLaneSurveillance))
		observer.ObserveInt64(due, urgentDue, urgent)
		observer.ObserveInt64(due, surveillanceDue, surveillance)
		observer.ObserveFloat64(age, urgentAge, urgent)
		observer.ObserveFloat64(age, surveillanceAge, surveillance)
		observer.ObserveInt64(quarantined, quarantineCount)
		observer.ObserveInt64(held, heldCount)
		return nil
	}, due, quarantined, age, held)
	return err
}
