package utils

import "context"

// WaitDone waits for the done signal or context cancellation.
func WaitDone(ctx context.Context, ch <-chan struct{}) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ch:
		return nil
	}
}
