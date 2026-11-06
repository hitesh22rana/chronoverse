//nolint:testpackage // Exercises the unexported lock mapping and the private delete path.
package workflow

import (
	"context"
	"testing"

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
