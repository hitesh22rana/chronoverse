//nolint:testpackage // Verifies the app's error logging and return contract.
package analyticsprocessor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type lifecycleContextKey struct{}

type boundaryLifecycleService struct {
	calls   int
	observe func(context.Context)
	err     error
}

func (s *boundaryLifecycleService) Run(ctx context.Context) error {
	s.calls++
	s.observe(ctx)
	return s.err
}

func TestRunPreservesServiceFailureAndLogsOutcome(t *testing.T) {
	for _, cause := range []error{nil, errors.New("worker failed"), context.Canceled} {
		core, logs := observer.New(zap.DebugLevel)
		var observed context.Context
		svc := &boundaryLifecycleService{err: cause, observe: func(ctx context.Context) { observed = ctx }}
		app := New(t.Context(), &Config{}, svc)
		app.logger = zap.New(core)
		ctx := context.WithValue(t.Context(), lifecycleContextKey{}, "parent")
		err := app.Run(ctx)
		require.ErrorIs(t, err, cause)
		require.Equal(t, 1, svc.calls)
		require.Equal(t, "parent", observed.Value(lifecycleContextKey{}))
		require.Len(t, logs.All(), 1)
		if cause == nil {
			require.Equal(t, zap.InfoLevel, logs.All()[0].Level)
		} else {
			require.Equal(t, zap.ErrorLevel, logs.All()[0].Level)
			require.Equal(t, cause.Error(), logs.All()[0].ContextMap()["error"])
		}
	}
}

func (*boundaryLifecycleService) CleanupProcessedEvents(context.Context, time.Duration, int) (int64, error) {
	panic("unexpected cleanup")
}
