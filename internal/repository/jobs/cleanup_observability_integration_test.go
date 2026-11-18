//nolint:testpackage // Real durable state owns backlog counts and redrive audit atomicity.
package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
)

func cleanupMetricsReader(t *testing.T, repo *Repository) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if err := repo.registerCleanupMetrics(provider.Meter("cleanup-test")); err != nil {
		t.Fatal(err)
	}
	return reader
}

func readCleanupMetrics(t *testing.T, reader *sdkmetric.ManualReader) map[string]float64 {
	t.Helper()
	var data metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &data); err != nil {
		t.Fatal(err)
	}
	result := map[string]float64{}
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			switch g := m.Data.(type) {
			case metricdata.Gauge[int64]:
				for _, point := range g.DataPoints {
					if point.Attributes.Len() != 0 {
						t.Fatal("cleanup identity leaked into labels")
					}
					result[m.Name] = float64(point.Value)
				}
			case metricdata.Gauge[float64]:
				for _, point := range g.DataPoints {
					if point.Attributes.Len() != 0 {
						t.Fatal("cleanup identity leaked into labels")
					}
					result[m.Name] = point.Value
				}
			}
		}
	}
	return result
}

//nolint:gocyclo // Follow one cleanup through delivery, abandonment, quarantine, restart, redrive and completion.
func TestIntegrationCleanupMetricsFollowDurableLifecycle(t *testing.T) {
	repo := isolatedContainerIntentRepository(t)
	fixture := seedRetainedCleanupOn(t, repo.pg)
	if _, err := repo.pg.Exec(t.Context(), `INSERT INTO outbox_events(topic,kafka_key,event_key,payload,status)
VALUES('workflows','ordinary','ordinary','{}','DEAD')`); err != nil {
		t.Fatal(err)
	}
	identity := jobsmodel.CleanupIdentity{
		JobID:           fixture.JobID,
		ContainerID:     fixture.ContainerID,
		RuntimeInstance: fixture.Instance,
		IntentID:        fixture.IntentID,
	}
	audit := jobsmodel.CleanupRedrive{
		Actor:  "operations/alice",
		Reason: "original daemon restored",
	}
	reader := cleanupMetricsReader(t, repo)
	check := func(outstanding, quarantined, dead float64) map[string]float64 {
		t.Helper()
		got := readCleanupMetrics(t, reader)
		if len(got) != 4 ||
			got["chronoverse.container_cleanup.outstanding"] != outstanding ||
			got["chronoverse.container_cleanup.quarantined"] != quarantined ||
			got["chronoverse.container_cleanup.publication_dead"] != dead {
			t.Fatalf("backlog metrics=%v", got)
		}
		return got
	}
	if _, err := repo.pg.Exec(t.Context(), `
		UPDATE outbox_events
		SET
			created_at = (clock_timestamp() AT TIME ZONE 'utc') - interval '2 hours',
			cleanup_replay_due_at = (clock_timestamp() AT TIME ZONE 'utc') + interval '1 hour'
		WHERE
			event_key = $1
	`, fixture.Key); err != nil {
		t.Fatal(err)
	}
	if got := check(1, 0, 0); got["chronoverse.container_cleanup.oldest_due"] < 7190 {
		t.Fatalf("future republication hid overdue execution: %v", got)
	}
	for attempt := 0; attempt < 3; attempt++ {
		first, err := repo.UpdateContainerCleanup(t.Context(), jobsmodel.CleanupOperationBegin, identity, 1, "", "", false, jobsmodel.CleanupRedrive{})
		if err != nil {
			t.Fatal(err)
		}
		if got := check(1, 0, 0); got["chronoverse.container_cleanup.oldest_due"] != 0 {
			t.Fatalf("live lease reported overdue: %v", got)
		}
		if attempt == 2 {
			if _, err = repo.pg.Exec(t.Context(), "UPDATE outbox_events SET cleanup_lease_until='1970-01-01' WHERE event_key=$1", fixture.Key); err != nil {
				t.Fatal(err)
			}
			if _, err = repo.UpdateContainerCleanup(t.Context(), jobsmodel.CleanupOperationBegin, identity, 1, "", "", false, jobsmodel.CleanupRedrive{}); err != nil {
				t.Fatal(err)
			}
			break
		}
		for range 2 {
			if _, err = repo.UpdateContainerCleanup(t.Context(), jobsmodel.CleanupOperationFail, identity, 1, first.Token, "Unavailable", false, jobsmodel.CleanupRedrive{}); err != nil {
				t.Fatal(err)
			}
			check(1, 0, 0)
		}
		if _, err = repo.pg.Exec(t.Context(), "UPDATE outbox_events SET cleanup_next_attempt_at='1970-01-01' WHERE event_key=$1", fixture.Key); err != nil {
			t.Fatal(err)
		}
	}
	if got := check(1, 1, 0); got["chronoverse.container_cleanup.oldest_due"] != 0 {
		t.Fatalf("quarantine mixed into automatic due age: %v", got)
	}
	reader = cleanupMetricsReader(t, repo) // A fresh exporter must recover counts from PostgreSQL.
	check(1, 1, 0)
	for range 2 {
		if _, err := repo.UpdateContainerCleanup(t.Context(), jobsmodel.CleanupOperationRedrive, identity, 1, "", "", false, audit); err != nil {
			t.Fatal(err)
		}
	}
	check(1, 0, 0)
	var actor, reason string
	var from, to int64
	var at time.Time
	if err := repo.pg.QueryRow(t.Context(), `
		SELECT
			actor,
			reason,
			expected_generation,
			generation,
			created_at
		FROM
			container_cleanup_redrives
		WHERE
			event_key = $1
	`, fixture.Key).Scan(&actor, &reason, &from, &to, &at); err != nil {
		t.Fatal(err)
	}
	if actor != audit.Actor || reason != audit.Reason || from != 1 || to != 2 || at.IsZero() {
		t.Fatalf("audit=%q %q %d/%d %v", actor, reason, from, to, at)
	}
	if _, err := repo.pg.Exec(t.Context(), "UPDATE outbox_events SET status='DEAD' WHERE event_key=$1", fixture.Key); err != nil {
		t.Fatal(err)
	}
	check(1, 0, 1)
	if err := repo.RecordContainerCleanupReceipt(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	got := check(0, 0, 0)
	if got["chronoverse.container_cleanup.oldest_due"] != 0 {
		t.Fatalf("completion retained overdue age: %v", got)
	}
	if _, err := repo.pg.Exec(t.Context(), "DELETE FROM outbox_events WHERE event_key=$1", fixture.Key); err != nil {
		t.Fatal(err)
	}
	var history int
	if err := repo.pg.QueryRow(t.Context(), "SELECT count(*) FROM container_cleanup_redrives WHERE event_key=$1", fixture.Key).Scan(&history); err != nil || history != 1 {
		t.Fatalf("audit lost/duplicated after pruning: count=%d error=%v", history, err)
	}
}

func TestIntegrationCleanupAuditRollsBackWithRedrive(t *testing.T) {
	for _, fault := range []struct {
		table, event string
		deferred     bool
	}{
		{"container_cleanup_redrives", "INSERT", false},
		{"outbox_events", "UPDATE", false},
		{"outbox_events", "UPDATE", true},
	} {
		t.Run(fault.table+fault.event+map[bool]string{true: " deferred", false: " immediate"}[fault.deferred], func(t *testing.T) {
			repo := isolatedContainerIntentRepository(t)
			fixture := seedRetainedCleanupOn(t, repo.pg)
			identity := jobsmodel.CleanupIdentity{
				JobID:           fixture.JobID,
				ContainerID:     fixture.ContainerID,
				RuntimeInstance: fixture.Instance,
				IntentID:        fixture.IntentID,
			}
			audit := jobsmodel.CleanupRedrive{
				Actor:  "operations/alice",
				Reason: "runtime repaired",
			}
			if _, err := repo.pg.Exec(t.Context(), `
				UPDATE outbox_events
				SET
					cleanup_replay_round = 8,
					cleanup_replay_due_at = now() + interval '1 hour'
				WHERE
					event_key = $1
			`, fixture.Key); err != nil {
				t.Fatal(err)
			}
			remove := scopedJobFaultTrigger(t.Context(), t, repo.pg, fault.table, fault.event, fault.deferred, map[string]string{"event_key": fixture.Key})
			if result, err := repo.UpdateContainerCleanup(t.Context(), jobsmodel.CleanupOperationRedrive, identity, 1, "", "", false, audit); err == nil || result != nil {
				t.Fatalf("partial redrive accepted: %+v %v", result, err)
			}
			remove()
			var generation, history int64
			var round int
			var due bool
			if err := repo.pg.QueryRow(t.Context(), `
				SELECT
					cleanup_generation,
					(
						SELECT
							count(*)
						FROM
							container_cleanup_redrives
						WHERE
							event_key = $1
					),
					cleanup_replay_round,
					cleanup_replay_due_at IS NOT NULL
				FROM
					outbox_events
				WHERE
					event_key = $1
			`, fixture.Key).Scan(&generation, &history, &round, &due); err != nil || generation != 1 || history != 0 || round != 8 || !due {
				t.Fatalf("partial audit persisted: generation=%d history=%d error=%v", generation, history, err)
			}
			if _, err := repo.UpdateContainerCleanup(t.Context(), jobsmodel.CleanupOperationRedrive, identity, 1, "", "", false, audit); err != nil {
				t.Fatal(err)
			}
			if err := repo.pg.QueryRow(t.Context(), `
				SELECT
					cleanup_replay_round,
					cleanup_replay_due_at IS NOT NULL
				FROM
					outbox_events
				WHERE
					event_key = $1
			`, fixture.Key).Scan(&round, &due); err != nil || round != 0 || due {
				t.Fatalf("accepted redrive retained pacing: %d/%v/%v", round, due, err)
			}
		})
	}
}

func TestIntegrationCleanupMetricsStorageFailureOmitsSamples(t *testing.T) {
	repo := runtimeSelectionRepository(t) // No outbox schema: query failure cannot mean a healthy zero.
	reader := cleanupMetricsReader(t, repo)
	var data metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &data); err == nil {
		t.Fatal("missing durable state reported success")
	}
	for _, scope := range data.ScopeMetrics {
		if len(scope.Metrics) != 0 {
			t.Fatalf("query failure fabricated samples: %+v", scope.Metrics)
		}
	}
}

func TestIntegrationCleanupMetricsRespectsCallerDeadline(t *testing.T) {
	repo := isolatedContainerIntentRepository(t)
	reader := cleanupMetricsReader(t, repo)
	readCleanupMetrics(t, reader) // A later failed collection must not reuse healthy samples.
	tx, err := repo.pg.BeginTx(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(context.Background()); rollbackErr != nil {
			t.Error(rollbackErr)
		}
	}()
	if _, err = tx.Exec(t.Context(), "LOCK TABLE outbox_events IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	var data metricdata.ResourceMetrics
	err = reader.Collect(ctx, &data)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 2*time.Second {
		t.Fatalf("collection ignored earlier caller deadline: elapsed=%s error=%v", time.Since(started), err)
	}
	for _, scope := range data.ScopeMetrics {
		if len(scope.Metrics) != 0 {
			t.Fatal("timed-out collection emitted healthy samples")
		}
	}
}
