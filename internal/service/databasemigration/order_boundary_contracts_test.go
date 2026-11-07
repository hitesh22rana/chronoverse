//nolint:testpackage // Asserts the service's own error span as well as call ordering.
package databasemigration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	otelcodes "go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/mock/gomock"

	mocks "github.com/hitesh22rana/chronoverse/internal/service/databasemigration/mock"
)

func TestMigrationOrderStopsAtFailureAndRecordsCause(t *testing.T) {
	for _, failed := range []string{"postgres", "clickhouse", "search", "none"} {
		t.Run(failed, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := mocks.NewMockRepository(ctrl)
			cause := errors.New("migration failed")
			var expected error
			var calls []any
			for _, name := range []string{"postgres", "clickhouse", "search"} {
				var err error
				if name == failed {
					err = cause
					expected = cause
				}
				switch name {
				case "postgres":
					calls = append(calls, repo.EXPECT().MigratePostgres(gomock.Any()).Return(err))
				case "clickhouse":
					calls = append(calls, repo.EXPECT().MigrateClickHouse(gomock.Any()).Return(err))
				case "search":
					calls = append(calls, repo.EXPECT().SetupMeiliSearch(gomock.Any()).Return(err))
				}
				if err != nil {
					break
				}
			}
			gomock.InOrder(calls...)
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
				defer cancel()
				require.NoError(t, provider.Shutdown(ctx))
			})
			service := New(repo)
			service.tp = provider.Tracer("migration-contract")
			err := service.Run(t.Context())
			require.ErrorIs(t, err, expected)
			spans := recorder.Ended()
			require.Len(t, spans, 1)
			if expected != nil {
				require.Equal(t, otelcodes.Error, spans[0].Status().Code)
				require.Equal(t, cause.Error(), spans[0].Status().Description)
				require.Len(t, spans[0].Events(), 1)
			} else {
				require.Empty(t, spans[0].Events())
			}
		})
	}
}
