//nolint:testpackage // Verifies exported measurements against committed ownership state.
package jobs

import (
	"context"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func collectIntentMetrics(t *testing.T, reader *sdkmetric.ManualReader) map[string]float64 {
	t.Helper()
	var result metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &result); err != nil {
		t.Fatal(err)
	}
	values := make(map[string]float64)
	for _, scope := range result.ScopeMetrics {
		if scope.Scope.Name != "chronoverse/jobs/container-intents" {
			continue
		}
		for _, measurement := range scope.Metrics {
			switch gauge := measurement.Data.(type) {
			case metricdata.Gauge[int64]:
				for _, point := range gauge.DataPoints {
					if point.Attributes.Len() != 0 {
						t.Fatal("identity leaked into lifecycle metric labels")
					}
					values[measurement.Name] = float64(point.Value)
				}
			case metricdata.Gauge[float64]:
				for _, point := range gauge.DataPoints {
					if point.Attributes.Len() != 0 {
						t.Fatal("identity leaked into lifecycle metric labels")
					}
					values[measurement.Name] = point.Value
				}
			}
		}
	}
	return values
}

func TestIntegrationContainerIntentMetricsReflectDurableQuarantine(t *testing.T) {
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	if err := repo.registerContainerIntentMetrics(provider.Meter("chronoverse/jobs/container-intents")); err != nil {
		t.Fatal(err)
	}
	before := collectIntentMetrics(t, reader)
	ctx := t.Context()
	user, workflow := seedUserWorkflow(ctx, t, pg)
	job, lease := seedRunningJob(ctx, t, pg, repo, workflow, user, t.Name())
	req := &jobsmodel.ContainerIntentCommand{
		JobID: job, LeaseToken: lease, Attempt: 1, WorkflowGeneration: 1,
		RuntimeInstance: "metrics-daemon", RequestHash: "immutable",
	}
	ensured, err := repo.UpdateContainerIntent(ctx, "ensure", req)
	if err != nil {
		t.Fatal(err)
	}
	req.IntentID = ensured.Intent.ID
	if _, err = repo.UpdateContainerIntent(ctx, "close", req); err != nil {
		t.Fatal(err)
	}
	due := collectIntentMetrics(t, reader)
	if due["chronoverse.container_intents.discovery_due"] < before["chronoverse.container_intents.discovery_due"]+1 {
		t.Fatalf("closed durable intent was absent from due gauge: before=%v after=%v", before, due)
	}
	if _, err = pg.Exec(ctx, "UPDATE container_creation_intents SET quarantined=true WHERE id=$1", req.IntentID); err != nil {
		t.Fatal(err)
	}
	quarantined := collectIntentMetrics(t, reader)
	if quarantined["chronoverse.container_intents.quarantined"] != before["chronoverse.container_intents.quarantined"]+1 {
		t.Fatalf("durable quarantine was absent from gauge: before=%v after=%v", before, quarantined)
	}
	if len(quarantined) != 4 || quarantined["chronoverse.container_intents.oldest_due"] < 0 {
		t.Fatalf("incomplete lifecycle measurements: %v", quarantined)
	}
}
