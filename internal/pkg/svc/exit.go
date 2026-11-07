package svc

import (
	"context"
	"errors"
	"fmt"
	"io"

	"go.uber.org/zap"

	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
)

// ExitCode reports an operation failure and maps it to a process exit status.
func ExitCode(err error, stderr io.Writer) int {
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// ServerExitCode treats a server stopped during caller cancellation as successful.
func ServerExitCode(ctx context.Context, err error, stderr io.Writer) int {
	if err != nil && ctx.Err() != nil {
		return 0
	}
	return ExitCode(err, stderr)
}

// WorkerExitCode accepts caller-requested shutdown without hiding unrelated errors.
func WorkerExitCode(ctx context.Context, err error, stderr io.Writer) int {
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return 0
	}
	return ExitCode(err, stderr)
}

// CloseAndLog reports cleanup failures without changing an operation's result.
func CloseAndLog(ctx context.Context, closeOperation func() error, message string) {
	if err := closeOperation(); err != nil {
		loggerpkg.FromContext(ctx).Warn(message, zap.Error(err))
	}
}
