package svc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

func TestCleanupReportsFailureOnceWithoutHidingOutcome(t *testing.T) {
	for _, cause := range []error{nil, errors.New("close failed")} {
		provider := sdklog.NewLoggerProvider()
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
			defer cancel()
			require.NoError(t, provider.Shutdown(ctx))
		})
		ctx, log := logger.Init(t.Context(), "cleanup-test", provider)
		core, entries := observer.New(zap.DebugLevel)
		*log = *log.WithOptions(zap.WrapCore(func(zapcore.Core) zapcore.Core { return core }))
		calls := 0
		svc.CloseAndLog(ctx, func() error { calls++; return cause }, "cleanup failed")
		require.Equal(t, 1, calls)
		if cause == nil {
			require.Empty(t, entries.All())
		} else {
			require.Len(t, entries.All(), 1)
			require.Equal(t, zap.WarnLevel, entries.All()[0].Level)
			require.Equal(t, "cleanup failed", entries.All()[0].Message)
			require.Equal(t, cause.Error(), entries.All()[0].ContextMap()["error"])
		}
	}
}
