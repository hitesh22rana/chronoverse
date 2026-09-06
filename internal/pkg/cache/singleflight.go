package cache

import (
	"context"
	"errors"

	"golang.org/x/sync/singleflight"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// WaitSingleflightResult waits for a shared result while respecting the caller's context.
func WaitSingleflightResult[T any](ctx context.Context, resultCh <-chan singleflight.Result) (T, error) {
	var zero T

	select {
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return zero, status.Error(codes.DeadlineExceeded, ctx.Err().Error())
		}

		return zero, status.Error(codes.Canceled, ctx.Err().Error())
	case result := <-resultCh:
		if result.Err != nil {
			return zero, result.Err
		}

		typed, ok := result.Val.(T)
		if !ok {
			return zero, status.Error(codes.Internal, "invalid singleflight result type")
		}

		return typed, nil
	}
}
