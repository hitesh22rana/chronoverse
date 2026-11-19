//nolint:testpackage // Verifies exported measurements against committed ownership state.
package jobs

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

// collectIntentMetrics keys each metric by lane; global series use the empty key.
// Any other label, or a duplicate lane series, fails the bounded-label contract.
func collectIntentMetrics(t *testing.T, reader *sdkmetric.ManualReader) map[string]map[string]float64 {
	t.Helper()
	var result metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &result); err != nil {
		t.Fatal(err)
	}
	values := make(map[string]map[string]float64)
	for _, scope := range result.ScopeMetrics {
		if scope.Scope.Name != "chronoverse/jobs/container-intents" {
			continue
		}
		for _, measurement := range scope.Metrics {
			series := make(map[string]float64)
			add := func(attrs attribute.Set, value float64) {
				lane := ""
				for _, kv := range attrs.ToSlice() {
					if kv.Key != containerIntentLaneLabel || lane != "" {
						t.Fatalf("%s: unbounded metric label %q", measurement.Name, kv.Key)
					}
					lane = kv.Value.AsString()
				}
				if _, duplicate := series[lane]; duplicate {
					t.Fatalf("%s: duplicate datapoint for lane %q", measurement.Name, lane)
				}
				series[lane] = value
			}
			switch gauge := measurement.Data.(type) {
			case metricdata.Gauge[int64]:
				for _, point := range gauge.DataPoints {
					add(point.Attributes, float64(point.Value))
				}
			case metricdata.Gauge[float64]:
				for _, point := range gauge.DataPoints {
					add(point.Attributes, point.Value)
				}
			}
			values[measurement.Name] = series
		}
	}
	return values
}

func intentSeries(t *testing.T, values map[string]map[string]float64, name string) map[string]float64 {
	t.Helper()
	series, ok := values[name]
	if !ok {
		t.Fatalf("metric %s was not collected: %v", name, values)
	}
	return series
}

// requireIntentCounts pins the exact series set and values, so an empty lane must
// be an explicit zero and an unlabeled total cannot reappear beside the lanes.
func requireIntentCounts(t *testing.T, values map[string]map[string]float64, name string, want map[string]float64) {
	t.Helper()
	series := intentSeries(t, values, name)
	if len(series) != len(want) {
		t.Fatalf("%s series = %v, want exactly %v", name, series, want)
	}
	for lane, expected := range want {
		value, found := series[lane]
		if !found || value != expected {
			t.Fatalf("%s lane %q = %v (found=%v), want %v", name, lane, value, found, expected)
		}
	}
}

func requireIntentAgeAtLeast(t *testing.T, values map[string]map[string]float64, lane string, minimum float64) float64 {
	t.Helper()
	series := intentSeries(t, values, "chronoverse.container_intents.oldest_due")
	if len(series) != 2 {
		t.Fatalf("oldest_due series = %v, want exactly two lanes", series)
	}
	value, found := series[lane]
	if !found || value < minimum {
		t.Fatalf("oldest_due lane %q = %v (found=%v), want >= %v seconds", lane, value, found, minimum)
	}
	return value
}

func newIntentMetricsReader(t *testing.T, repo *Repository) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if err := repo.registerContainerIntentMetrics(provider.Meter("chronoverse/jobs/container-intents")); err != nil {
		t.Fatal(err)
	}
	return reader
}

func TestIntegrationContainerIntentMetricsLaneSeries(t *testing.T) {
	repo := isolatedContainerIntentRepository(t)
	reader := newIntentMetricsReader(t, repo)
	ctx := t.Context()
	urgent, surveillance := jobsmodel.ContainerInspectionLaneUrgent, jobsmodel.ContainerInspectionLaneSurveillance

	empty := collectIntentMetrics(t, reader)
	if len(empty) != 4 {
		t.Fatalf("lifecycle measurement set = %v, want exactly four gauges", empty)
	}
	requireIntentCounts(t, empty, "chronoverse.container_intents.discovery_due", map[string]float64{urgent: 0, surveillance: 0})
	requireIntentCounts(t, empty, "chronoverse.container_intents.oldest_due", map[string]float64{urgent: 0, surveillance: 0})
	requireIntentCounts(t, empty, "chronoverse.container_intents.quarantined", map[string]float64{"": 0})
	requireIntentCounts(t, empty, "chronoverse.container_intents.capacity_holds", map[string]float64{"": 0})

	seedIntent := func(tag string) string {
		t.Helper()
		user := testkit.SeedUser(ctx, t, repo.pg, "metrics-"+tag+"@chronoverse.test")
		workflow := testkit.SeedWorkflow(ctx, t, repo.pg, user, "metrics-"+tag)
		job, lease := seedRunningJob(ctx, t, repo.pg, repo, workflow, user, tag)
		req := &jobsmodel.ContainerIntentCommand{
			JobID: job, LeaseToken: lease, Attempt: 1, WorkflowGeneration: 1,
			RuntimeInstance: "metrics-daemon", RequestHash: "immutable",
		}
		ensured, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationEnsure, req)
		if err != nil {
			t.Fatal(err)
		}
		req.IntentID = ensured.Intent.ID
		if _, err = repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationClose, req); err != nil {
			t.Fatal(err)
		}
		return req.IntentID
	}

	urgentID := seedIntent(t.Name() + "-urgent")
	if _, err := repo.pg.Exec(ctx, `
		UPDATE container_creation_intents
		SET
			next_inspection_at = (clock_timestamp() AT TIME ZONE 'utc') - interval '10 seconds'
		WHERE
			id = $1
	`, urgentID); err != nil {
		t.Fatal(err)
	}
	one := collectIntentMetrics(t, reader)
	requireIntentCounts(t, one, "chronoverse.container_intents.discovery_due", map[string]float64{urgent: 1, surveillance: 0})
	urgentAge := requireIntentAgeAtLeast(t, one, urgent, 9)
	if surveillanceAge := requireIntentAgeAtLeast(t, one, surveillance, 0); surveillanceAge != 0 {
		t.Fatalf("empty surveillance lane reported age %v, want explicit zero", surveillanceAge)
	}

	surveillanceID := seedIntent(t.Name() + "-surveillance")
	if _, err := repo.pg.Exec(ctx, `
		UPDATE container_creation_intents
		SET
			inspection_urgent = false,
			next_inspection_at = (clock_timestamp() AT TIME ZONE 'utc') - interval '60 seconds'
		WHERE
			id = $1
	`, surveillanceID); err != nil {
		t.Fatal(err)
	}
	both := collectIntentMetrics(t, reader)
	requireIntentCounts(t, both, "chronoverse.container_intents.discovery_due", map[string]float64{urgent: 1, surveillance: 1})
	if age := requireIntentAgeAtLeast(t, both, surveillance, 59); age <= urgentAge {
		t.Fatalf("surveillance age %v did not exceed urgent age %v", age, urgentAge)
	}

	if _, err := repo.pg.Exec(ctx, "UPDATE container_creation_intents SET quarantined=true WHERE id=$1", surveillanceID); err != nil {
		t.Fatal(err)
	}
	quarantined := collectIntentMetrics(t, reader)
	requireIntentCounts(t, quarantined, "chronoverse.container_intents.discovery_due", map[string]float64{urgent: 1, surveillance: 0})
	requireIntentCounts(t, quarantined, "chronoverse.container_intents.quarantined", map[string]float64{"": 1})
	requireIntentAgeAtLeast(t, quarantined, urgent, urgentAge)
	if ageSeries := intentSeries(t, quarantined, "chronoverse.container_intents.oldest_due"); len(ageSeries) != 2 || ageSeries[surveillance] != 0 {
		t.Fatalf("quarantined surveillance lane was not an explicit zero: %v", ageSeries)
	}

	// A failed read must not fall back to healthy zero lanes for an empty sample.
	if _, err := repo.pg.Exec(ctx, `
		ALTER TABLE container_creation_intents
		RENAME COLUMN inspection_urgent TO unavailable_inspection_urgent
	`); err != nil {
		t.Fatal(err)
	}
	var failed metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &failed); err == nil {
		t.Fatal("unavailable lane state was reported as a healthy zero backlog")
	}
	for _, scope := range failed.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			t.Fatalf("failed collection fabricated a measurement: %s", measurement.Name)
		}
	}
}
