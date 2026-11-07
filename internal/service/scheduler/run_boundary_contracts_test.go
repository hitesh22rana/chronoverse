//nolint:testpackage // Verifies the service's own trace event and error span.
package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	otelcodes "go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type resultRepository struct {
	total int
	err   error
	calls int
}

func (r *resultRepository) Run(context.Context) (int, error) { r.calls++; return r.total, r.err }
func TestRunReturnsCountOrOriginalErrorAndRecordsTrace(t *testing.T) {
	for _, cause := range []error{nil, errors.New("schedule failed")} {
		repo := &resultRepository{total: 7, err: cause}
		recorder := tracetest.NewSpanRecorder()
		provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
			defer cancel()
			require.NoError(t, provider.Shutdown(ctx))
		})
		service := New(repo)
		service.tp = provider.Tracer("scheduler-contract")
		total, err := service.Run(t.Context())
		require.ErrorIs(t, err, cause)
		require.Equal(t, 1, repo.calls)
		spans := recorder.Ended()
		require.Len(t, spans, 1)
		require.Len(t, spans[0].Events(), 1)
		if cause == nil {
			require.Equal(t, 7, total)
			require.Equal(t, "scheduled jobs", spans[0].Events()[0].Name)
			require.Equal(t, int64(7), spans[0].Events()[0].Attributes[0].Value.AsInt64())
		} else {
			require.Zero(t, total)
			require.Equal(t, otelcodes.Error, spans[0].Status().Code)
			require.Equal(t, cause.Error(), spans[0].Status().Description)
		}
	}
}
