package workflow

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const lifecycleLockRenewalTimeout = 5 * time.Second

func (r *Repository) withLifecycleLock(parentCtx context.Context, key string, ttl time.Duration, work func(context.Context) error) (err error) {
	token, acquired, err := r.rdb.AcquireDistributedLockWithToken(parentCtx, key, ttl)
	if err != nil || !acquired {
		return distributedLockError(parentCtx)
	}
	ctx, cancel := context.WithCancelCause(parentCtx)
	done := make(chan error, 1)
	go func() { done <- r.renewLifecycleLock(ctx, cancel, key, token, ttl) }()
	defer func() {
		cancel(nil)
		renewErr := <-done
		r.releaseLifecycleLock(parentCtx, key, token)
		if renewErr != nil {
			err = renewErr
		}
	}()
	return work(ctx)
}

func (r *Repository) renewLifecycleLock(ctx context.Context, cancel context.CancelCauseFunc, key, token string, ttl time.Duration) error {
	interval := ttl / 3
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case tick := <-ticker.C:
			// Finish the renewal before the remaining lease budget can be exhausted.
			deadline := tick.Add(min(interval, lifecycleLockRenewalTimeout))
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
		}
	}
}
