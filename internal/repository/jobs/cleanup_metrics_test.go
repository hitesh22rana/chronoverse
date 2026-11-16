//nolint:testpackage // Initialization errors must be reported rather than silently disable alert metrics.
package jobs

import (
	"errors"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

type cleanupRejectingProvider struct {
	metric.MeterProvider
	err error
}

func (p cleanupRejectingProvider) Meter(string, ...metric.MeterOption) metric.Meter {
	return intentRejectingMeter{Meter: noop.NewMeterProvider().Meter("test"), fail: "chronoverse.container_cleanup.outstanding", err: p.err}
}

func TestCleanupMetricsInitializationReportsFailure(t *testing.T) {
	failure := errors.New("cleanup metrics rejected")
	for _, name := range []string{
		"chronoverse.container_cleanup.outstanding",
		"chronoverse.container_cleanup.quarantined",
		"chronoverse.container_cleanup.publication_dead",
		"chronoverse.container_cleanup.oldest_due",
		"callback",
	} {
		t.Run(name, func(t *testing.T) {
			meter := intentRejectingMeter{Meter: noop.NewMeterProvider().Meter("test"), fail: name, err: failure}
			if err := (&Repository{}).registerCleanupMetrics(meter); !errors.Is(err, failure) {
				t.Fatalf("initialization failure hidden: %v", err)
			}
		})
	}
	previousProvider, previousHandler := otel.GetMeterProvider(), otel.GetErrorHandler()
	defer otel.SetMeterProvider(previousProvider)
	defer otel.SetErrorHandler(previousHandler)
	otel.SetMeterProvider(noop.NewMeterProvider())
	otel.SetMeterProvider(cleanupRejectingProvider{MeterProvider: noop.NewMeterProvider(), err: failure})
	reported := 0
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		if errors.Is(err, failure) {
			reported++
		}
	}))
	if repo := New(&Config{}, nil, &postgres.Postgres{}, nil, nil, nil, nil); repo == nil || reported != 1 {
		t.Fatalf("repository=%v reported=%d", repo, reported)
	}
}

func TestCleanupRedriveRejectsMissingAuditBeforeStorage(t *testing.T) {
	for _, audit := range []jobsmodel.CleanupRedrive{
		{},
		{Actor: "operations/alice"},
		{Reason: "repaired"},
		{Actor: " ", Reason: "repaired"},
		{Actor: "operations/alice", Reason: "\n"},
		{Actor: strings.Repeat("a", 257), Reason: "repaired"},
		{Actor: "operations/alice", Reason: strings.Repeat("a", 2049)},
	} {
		result, err := (&Repository{}).UpdateContainerCleanup(t.Context(), jobsmodel.CleanupOperationRedrive, jobsmodel.CleanupIdentity{
			JobID:           "job",
			ContainerID:     "container",
			RuntimeInstance: "original",
			IntentID:        "intent",
		}, 1, "", "", false, audit)
		if result != nil || status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid audit reached storage: %+v error=%v", audit, err)
		}
	}
}
