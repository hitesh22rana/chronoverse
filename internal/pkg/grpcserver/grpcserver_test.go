package grpcserver_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"

	grpcserverpkg "github.com/hitesh22rana/chronoverse/internal/pkg/grpcserver"
)

// TestGracefulStopDrains ensures that GracefulStop returns promptly once the
// underlying grpc.Server.GracefulStop finishes, without waiting for the
// timeout. It exercises the happy path: no in-flight RPCs, the drain
// returns immediately, and the function exits well before the 5s budget.
func TestGracefulStopDrains(t *testing.T) {
	server := grpc.NewServer()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		grpcserverpkg.GracefulStop(ctx, server, 5*time.Second)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("GracefulStop did not return after server.GracefulStop finished")
	}
}

// TestGracefulStopWithImmediateDeadlineReturns covers the boundary where the
// drain completion and timeout are both immediately eligible. Either selected
// path must stop the server and return without blocking.
func TestGracefulStopWithImmediateDeadlineReturns(t *testing.T) {
	server := grpc.NewServer()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		grpcserverpkg.GracefulStop(ctx, server, 0)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("GracefulStop did not return")
	}
}
