package main

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

	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
)

type (
	drainContextKey struct{}
	boundaryDrainer struct {
		observe func(context.Context)
		err     error
		calls   int
	}
)

func (d *boundaryDrainer) MarkDraining(ctx context.Context) error {
	d.observe(ctx)
	d.calls++
	return d.err
}

func TestDrainUsesDetachedBoundedContextAndReportsFailure(t *testing.T) {
	for _, cause := range []error{nil, errors.New("database unavailable")} {
		provider := sdklog.NewLoggerProvider()
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
			defer cancel()
			require.NoError(t, provider.Shutdown(ctx))
		})
		parent, log := loggerpkg.Init(t.Context(), "drain-contract", provider)
		core, entries := observer.New(zap.DebugLevel)
		*log = *log.WithOptions(zap.WrapCore(func(zapcore.Core) zapcore.Core { return core }))
		parent = context.WithValue(parent, drainContextKey{}, "caller")
		ctx, cancel := context.WithCancel(parent)
		cancel()
		var observed context.Context
		var errAtCall error
		repo := &boundaryDrainer{err: cause, observe: func(ctx context.Context) { observed = ctx; errAtCall = ctx.Err() }}
		drainRuntime(ctx, repo)
		require.Equal(t, 1, repo.calls)
		require.NoError(t, errAtCall)
		require.Equal(t, "caller", observed.Value(drainContextKey{}))
		deadline, ok := observed.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), 5*time.Second)
		require.Positive(t, time.Until(deadline))
		require.ErrorIs(t, observed.Err(), context.Canceled)
		if cause == nil {
			require.Empty(t, entries.All())
		} else {
			require.Len(t, entries.All(), 1)
			require.Equal(t, zap.WarnLevel, entries.All()[0].Level)
			require.Equal(t, cause.Error(), entries.All()[0].ContextMap()["error"])
		}
	}
}
