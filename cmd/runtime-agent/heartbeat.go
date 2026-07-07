package main

import (
	"context"
	"time"

	"go.uber.org/zap"

	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
)

type runtimeNodeStore interface {
	RegisterReady(ctx context.Context) error
	Heartbeat(ctx context.Context) error
	MarkUnhealthy(ctx context.Context) error
}

type dockerHealthChecker interface {
	Healthy(ctx context.Context) error
}

func startRuntimeHeartbeats(
	ctx context.Context,
	repo runtimeNodeStore,
	health dockerHealthChecker,
	interval time.Duration,
) error {
	if err := health.Healthy(ctx); err != nil {
		return err
	}
	return runHealthCheckedHeartbeats(ctx, repo, health, interval)
}

func runHealthCheckedHeartbeats(
	ctx context.Context,
	repo runtimeNodeStore,
	health dockerHealthChecker,
	interval time.Duration,
) error {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if err := repo.RegisterReady(ctx); err != nil {
		return err
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := heartbeatOnce(ctx, repo, health); err != nil {
				return err
			}
		}
	}
}

func heartbeatOnce(ctx context.Context, repo runtimeNodeStore, health dockerHealthChecker) error {
	if err := health.Healthy(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		loggerpkg.FromContext(ctx).Warn("runtime Docker endpoint is unhealthy", zap.Error(err))
		return repo.MarkUnhealthy(ctx)
	}
	return repo.Heartbeat(ctx)
}
