package main

import (
	"context"
	"os"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// firewallReadyMaxAge bounds ready-marker staleness: refreshed every minute,
	// 5m tolerates a few missed loops before the node leaves scheduling.
	firewallReadyMaxAge = 5 * time.Minute
)

// combinedHealthChecker passes only if every checker passes.
type combinedHealthChecker struct {
	checkers []dockerHealthChecker
}

func (c combinedHealthChecker) Healthy(ctx context.Context) error {
	for _, checker := range c.checkers {
		if err := checker.Healthy(ctx); err != nil {
			return err
		}
	}
	return nil
}

// firewallReadyChecker keeps unfirewalled nodes out of scheduling: a missing
// or stale marker means egress filtering isn't enforced. Empty path disables.
type firewallReadyChecker struct {
	path   string
	maxAge time.Duration
	now    func() time.Time
}

func (c firewallReadyChecker) Healthy(context.Context) error {
	if c.path == "" {
		return nil
	}
	info, err := os.Stat(c.path)
	if err != nil {
		return status.Errorf(codes.Unavailable, "workload firewall not ready: %v", err)
	}
	now := time.Now()
	if c.now != nil {
		now = c.now()
	}
	if age := now.Sub(info.ModTime()); age > c.maxAge {
		return status.Errorf(codes.Unavailable, "workload firewall ready marker stale (%v)", age.Round(time.Second))
	}
	return nil
}
