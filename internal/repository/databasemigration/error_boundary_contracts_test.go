//nolint:testpackage // Verifies repository-owned spans and configuration failures.
package databasemigration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	otelcodes "go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMigrationConfigurationFailuresKeepTracingError(t *testing.T) {
	for _, database := range []string{"postgres", "clickhouse"} {
		t.Run(database, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
				defer cancel()
				require.NoError(t, provider.Shutdown(ctx))
			})
			repo := New(&Config{PostgresDSN: "postgres://localhost/unused?sslmode=invalid"})
			repo.tp = provider.Tracer("migration-errors")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var err error
			if database == "postgres" {
				err = repo.MigratePostgres(ctx)
				require.ErrorContains(t, err, "postgres migration failed after retries")
			} else {
				cancel()
				err = repo.MigrateClickHouse(ctx)
				require.ErrorContains(t, err, "clickhouse migration failed after retries")
				require.ErrorContains(t, err, context.Canceled.Error())
			}
			require.Equal(t, codes.Internal, status.Code(err))
			spans := recorder.Ended()
			require.Len(t, spans, 1)
			require.Equal(t, otelcodes.Error, spans[0].Status().Code)
			require.Equal(t, err.Error(), spans[0].Status().Description)
			require.Len(t, spans[0].Events(), 1)
		})
	}
}
