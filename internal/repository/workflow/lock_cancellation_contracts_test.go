//nolint:testpackage // Exercises the unexported lock mapping and the private delete path.
package workflow

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDistributedLockErrorMapping(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		canceled bool
		want     codes.Code
	}{
		{name: "healthy caller", want: codes.Aborted},
		{name: "canceled caller", canceled: true, want: codes.Canceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			if tt.canceled {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}

			if got := status.Code(distributedLockError(ctx)); got != tt.want {
				t.Fatalf("distributedLockError() code = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestDeleteWorkflowLockAcquisitionPreservesCallerCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// The Redis store wraps lock failures in a gRPC status, so only the caller's
	// context still carries the cancellation.
	lock := &lifecycleLock{err: status.Error(codes.Internal, "failed to acquire lock: context canceled")}
	err := (&Repository{rdb: lock}).deleteWorkflow(ctx, "workflow-1", "user-1")
	if got := status.Code(err); got != codes.Canceled {
		t.Fatalf("deleteWorkflow() code = %s, want %s: %v", got, codes.Canceled, err)
	}
	if len(lock.released) != 0 {
		t.Fatalf("deleteWorkflow() released a lock it never owned: %v", lock.released)
	}
}

type releaseContextLock struct {
	workflowLockStore
	release func(context.Context, string, string) error
}

func (s *releaseContextLock) ReleaseDistributedLockWithToken(ctx context.Context, key, token string) error {
	return s.release(ctx, key, token)
}

func TestLifecycleLockReleaseHasBoundedDetachedContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var releaseCtx context.Context
	lock := &releaseContextLock{release: func(ctx context.Context, key, token string) error {
		releaseCtx = ctx
		if ctx.Err() != nil {
			t.Fatalf("release inherited caller cancellation: %v", ctx.Err())
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 5*time.Second {
			t.Fatalf("release deadline = %v, present=%v", deadline, ok)
		}
		if key != "workflow:RESCHEDULE:one" || token != "original-owner" {
			t.Fatalf("release identity = %q, %q", key, token)
		}
		return status.Error(codes.Unavailable, "Redis unavailable")
	}}
	(&Repository{rdb: lock}).releaseLifecycleLock(ctx, "workflow:RESCHEDULE:one", "original-owner")
	if releaseCtx == nil || releaseCtx.Err() != context.Canceled {
		t.Fatal("release context was not canceled after cleanup")
	}
}
