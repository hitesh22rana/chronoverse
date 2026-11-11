package logger_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
)

type diagnosticExporter struct{ records []sdklog.Record }

func (e *diagnosticExporter) Export(_ context.Context, records []sdklog.Record) error {
	for i := range records {
		e.records = append(e.records, records[i].Clone())
	}
	return nil
}
func (*diagnosticExporter) Shutdown(context.Context) error   { return nil }
func (*diagnosticExporter) ForceFlush(context.Context) error { return nil }

func TestTraceContextCorrelatesBothLogPathsWithoutDisclosure(t *testing.T) {
	exporter := &diagnosticExporter{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exporter)))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.WithoutCancel(t.Context()))) })
	var output bytes.Buffer
	logger := zap.New(zapcore.NewTee(
		zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(&output), zap.InfoLevel),
		otelzap.NewCore("diagnostic-contract", otelzap.WithLoggerProvider(provider)),
	))
	type privateContextKey struct{}
	ctx := context.WithValue(t.Context(), privateContextKey{}, "private-context-value")
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled})
	ctx = trace.ContextWithSpanContext(ctx, spanContext)
	logger.Info("record processed", loggerpkg.TraceContext(ctx), zap.String("event_key", "event-1"))
	logger.With(loggerpkg.TraceContext(ctx)).Info("request completed", zap.String("event_key", "event-2"))
	require.NotContains(t, output.String(), "private-context-value")
	require.NotContains(t, output.String(), `"ctx"`)
	require.Contains(t, output.String(), "event-1")
	require.Contains(t, output.String(), "event-2")
	require.Len(t, exporter.records, 2)
	for i, record := range exporter.records {
		require.Equal(t, spanContext.TraceID(), record.TraceID())
		require.Equal(t, spanContext.SpanID(), record.SpanID())
		var fields []attribute.KeyValue
		record.WalkAttributes(func(kv attribute.KeyValue) bool { fields = append(fields, kv); return true })
		require.NotContains(t, fmt.Sprint(fields), "private-context-value")
		require.Contains(t, fmt.Sprint(fields), fmt.Sprintf("event-%d", i+1))
	}
}
