package svc_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

func TestExitCodeReportsFailuresWithoutLosingDiagnostic(t *testing.T) {
	cause := errors.New("operation failed")
	var diagnostic bytes.Buffer
	require.Equal(t, 0, svc.ExitCode(nil, &diagnostic))
	require.Empty(t, diagnostic.String())
	require.Equal(t, 1, svc.ExitCode(cause, &diagnostic))
	require.Equal(t, "operation failed\n", diagnostic.String())
}

func TestServerExitDistinguishesShutdownFromUnexpectedFailure(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		for _, cause := range []error{nil, errors.New("server stopped")} {
			ctx, cancel := context.WithCancel(t.Context())
			if canceled {
				cancel()
			}
			var diagnostic bytes.Buffer
			code := svc.ServerExitCode(ctx, cause, &diagnostic)
			cancel()
			if cause != nil && !canceled {
				require.Equal(t, 1, code)
				require.Equal(t, "server stopped\n", diagnostic.String())
			} else {
				require.Zero(t, code)
				require.Empty(t, diagnostic.String())
			}
		}
	}
}

func TestWorkerExitOnlyIgnoresTheCallersCancellation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		canceled   bool
		cause      error
		want       int
		diagnostic string
	}{
		{name: "success"},
		{name: "live caller cancellation error", cause: context.Canceled, want: 1, diagnostic: "context canceled\n"},
		{name: "requested shutdown", canceled: true, cause: context.Canceled},
		{name: "wrapped requested shutdown", canceled: true, cause: fmt.Errorf("worker stopped: %w", context.Canceled)},
		{name: "unrelated shutdown failure", canceled: true, cause: errors.New("unrelated failure"), want: 1, diagnostic: "unrelated failure\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			var diagnostic bytes.Buffer
			require.Equal(t, tc.want, svc.WorkerExitCode(ctx, tc.cause, &diagnostic))
			require.Equal(t, tc.diagnostic, diagnostic.String())
		})
	}
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	var diagnostic bytes.Buffer
	require.Zero(t, svc.WorkerExitCode(ctx, context.DeadlineExceeded, &diagnostic))
	require.Empty(t, diagnostic.String())
}
