//nolint:testpackage // Exercises diagnostics at the consumer boundary.
package joblogs

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc/codes"

	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
)

func TestConsumerDiagnosticsExcludeRecordContents(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		code        codes.Code
	}{
		{name: "missing event identity", value: `{"Message":"private-payload"}`, code: codes.OK},
		{name: "malformed event", value: `{"SequenceNum":123456789012345678901234567890,"Message":"private-payload"}`, code: codes.OK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := sdklog.NewLoggerProvider()
			t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.WithoutCancel(t.Context()))) })
			ctx, logger := loggerpkg.Init(t.Context(), "diagnostic-contract", provider)
			core, entries := observer.New(zap.InfoLevel)
			*logger = *logger.WithOptions(zap.WrapCore(func(zapcore.Core) zapcore.Core { return core }))
			type privateContextKey struct{}
			ctx = context.WithValue(ctx, privateContextKey{}, "private-context")
			tracerProvider := sdktrace.NewTracerProvider()
			t.Cleanup(func() { require.NoError(t, tracerProvider.Shutdown(context.WithoutCancel(t.Context()))) })
			repo := &Repository{tp: tracerProvider.Tracer("diagnostic-contract")}
			record := &kgo.Record{Topic: "test-topic", Partition: 2, Offset: 17, Value: []byte(tc.value)}
			require.Nil(t, repo.parseRecord(ctx, record))
			require.Len(t, entries.All(), 1)
			fields := entries.All()[0].ContextMap()
			diagnostic := fmt.Sprint(fields)
			for _, forbidden := range []string{"private-payload", "private-context", "123456789012345678901234567890"} {
				require.NotContains(t, diagnostic, forbidden)
			}
			require.NotContains(t, fields, "ctx")
			require.NotContains(t, fields, "value")
			require.NotContains(t, fields, "event")
			require.Equal(t, "test-topic", fields["topic"])
			require.EqualValues(t, 2, fields["partition"])
			require.EqualValues(t, 17, fields["offset"])
			if tc.name == "malformed event" {
				require.Equal(t, "*json.UnmarshalTypeError", fields["error_type"])
			}
		})
	}
}
