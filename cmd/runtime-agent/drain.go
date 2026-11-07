package main

import (
	"context"
	"time"

	"go.uber.org/zap"

	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
)

type runtimeDrainer interface{ MarkDraining(context.Context) error }

// drainRuntime keeps shutdown bookkeeping bounded after the run context is canceled.
func drainRuntime(ctx context.Context, repo runtimeDrainer) {
	drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := repo.MarkDraining(drainCtx); err != nil {
		loggerpkg.FromContext(ctx).Warn("failed to mark runtime node draining", zap.Error(err))
	}
}
