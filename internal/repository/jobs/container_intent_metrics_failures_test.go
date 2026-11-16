//nolint:testpackage // Telemetry failure must not fabricate successful measurements or block repository construction.
package jobs

import (
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

type intentRejectingMeter struct {
	metric.Meter
	fail string
	err  error
}

func (m intentRejectingMeter) Int64ObservableGauge(name string, options ...metric.Int64ObservableGaugeOption) (metric.Int64ObservableGauge, error) {
	if name == m.fail {
		return nil, m.err
	}
	return m.Meter.Int64ObservableGauge(name, options...)
}

func (m intentRejectingMeter) Float64ObservableGauge(name string, options ...metric.Float64ObservableGaugeOption) (metric.Float64ObservableGauge, error) {
	if name == m.fail {
		return nil, m.err
	}
	return m.Meter.Float64ObservableGauge(name, options...)
}

func (m intentRejectingMeter) RegisterCallback(f metric.Callback, instruments ...metric.Observable) (metric.Registration, error) {
	if m.fail == "callback" {
		return nil, m.err
	}
	return m.Meter.RegisterCallback(f, instruments...)
}

type intentRejectingProvider struct {
	metric.MeterProvider
	err error
}

func (p intentRejectingProvider) Meter(string, ...metric.MeterOption) metric.Meter {
	return intentRejectingMeter{Meter: noop.NewMeterProvider().Meter("test"), fail: "chronoverse.container_intents.discovery_due", err: p.err}
}

func TestContainerIntentMetricsInitializationDoesNotHideFailure(t *testing.T) {
	failure := errors.New("telemetry rejected instrument")
	for _, name := range []string{
		"chronoverse.container_intents.discovery_due",
		"chronoverse.container_intents.quarantined",
		"chronoverse.container_intents.oldest_due",
		"chronoverse.container_intents.capacity_holds",
		"callback",
	} {
		t.Run(name, func(t *testing.T) {
			meter := intentRejectingMeter{Meter: noop.NewMeterProvider().Meter("test"), fail: name, err: failure}
			if err := (&Repository{}).registerContainerIntentMetrics(meter); !errors.Is(err, failure) {
				t.Fatalf("telemetry initialization failure hidden: %v", err)
			}
		})
	}
	previousProvider, previousHandler := otel.GetMeterProvider(), otel.GetErrorHandler()
	defer otel.SetMeterProvider(previousProvider)
	defer otel.SetErrorHandler(previousHandler)
	reported := 0
	otel.SetMeterProvider(noop.NewMeterProvider())
	otel.SetMeterProvider(intentRejectingProvider{MeterProvider: noop.NewMeterProvider(), err: failure})
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		if errors.Is(err, failure) {
			reported++
		}
	}))
	repo := New(&Config{}, nil, &postgres.Postgres{}, nil, nil, nil, nil)
	if repo == nil || reported != 1 {
		t.Fatalf("repository construction lost telemetry diagnostic: repository=%v errors=%d", repo, reported)
	}
}
