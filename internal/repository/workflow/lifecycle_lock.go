package workflow

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (r *Repository) withLifecycleLock(
	parentCtx context.Context,
	key string,
	ttl time.Duration,
	work func(context.Context) error,
) (
	err error,
) {
	if ttl/3 <= 0 {
		return status.Error(codes.InvalidArgument, "workflow lock TTL must permit a positive renewal interval")
	}
	expiresAt := time.Now().Add(ttl)
	token, acquired, err := r.rdb.AcquireDistributedLockWithToken(parentCtx, key, ttl)
	if err != nil || !acquired {
		return distributedLockError(parentCtx)
	}
	ctx, cancel := context.WithCancelCause(parentCtx)
	done := make(chan error, 1)
	go func() { done <- r.renewLifecycleLock(ctx, cancel, key, token, ttl, expiresAt) }()
	defer func() {
		cancel(nil)
		renewErr := <-done
		r.releaseLifecycleLock(parentCtx, key, token)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			err = status.FromContextError(err).Err()
		}
		if renewErr != nil && (err == nil || status.Code(err) == codes.Canceled || status.Code(err) == codes.DeadlineExceeded) {
			err = renewErr
		}
	}()
	return work(ctx)
}

func (r *Repository) renewLifecycleLock(
	ctx context.Context,
	cancel context.CancelCauseFunc,
	key,
	token string,
	ttl time.Duration,
	expiresAt time.Time,
) error {
	interval := ttl / 3
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			started := time.Now()
			// Reserve a third of the lease for cancellation; never borrow time from an expired lease.
			deadline := started.Add(interval)
			if latest := expiresAt.Add(-interval); latest.Before(deadline) {
				deadline = latest
			}
			if !deadline.After(started) {
				err := status.Error(codes.Unavailable, "workflow lock renewal budget exhausted")
				cancel(err)
				return err
			}
			renewCtx, stop := context.WithDeadline(ctx, deadline)
			renewed, err := r.rdb.ExtendDistributedLockWithToken(renewCtx, key, token, ttl)
			stop()
			if ctx.Err() != nil {
				return nil
			}
			if err != nil {
				err = status.Errorf(codes.Unavailable, "workflow lock renewal failed: %v", err)
			} else if !renewed {
				err = status.Error(codes.Aborted, "workflow lock ownership was lost")
			}
			if err != nil {
				cancel(err)
				return err
			}
			// Redis applies expiry after the request starts, so this is a conservative lower bound.
			expiresAt = started.Add(ttl)
		}
	}
}
