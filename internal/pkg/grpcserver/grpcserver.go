package grpcserver

import (
	"context"
	"time"

	"google.golang.org/grpc"
)

// GracefulStop drains a gRPC server after ctx is canceled, with a bounded
// fallback so Kubernetes termination can always complete.
//
// On a normal shutdown the caller's signal-aware context is canceled, every
// in-flight RPC finishes, the listener closes, and the server stops within
// the deadline. If draining stalls, the function falls back to grpc.Server.Stop
// after `timeout` to guarantee progress.
func GracefulStop(ctx context.Context, server *grpc.Server, timeout time.Duration) {
	<-ctx.Done()

	done := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(done)
	}()

	// Keep an explicit timer so the fast drain path can release its resources
	// immediately instead of leaving the timeout armed unnecessarily.
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-done:
	case <-timer.C:
		server.Stop()
	}
}
