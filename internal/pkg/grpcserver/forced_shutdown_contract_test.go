package grpcserver_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	grpcserverpkg "github.com/hitesh22rana/chronoverse/internal/pkg/grpcserver"
)

type blockedShutdownHealth struct {
	healthpb.UnimplementedHealthServer
	started, finished chan struct{}
}

func (s *blockedShutdownHealth) Check(ctx context.Context, _ *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	close(s.started)
	defer close(s.finished)
	<-ctx.Done()
	return nil, status.FromContextError(ctx.Err()).Err()
}

func TestGracefulStopForcesAnActiveRPCToFinish(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	service := &blockedShutdownHealth{started: make(chan struct{}), finished: make(chan struct{})}
	server := grpc.NewServer()
	t.Cleanup(server.Stop)
	healthpb.RegisterHealthServer(server, service)
	listener := bufconn.Listen(64 * 1024)
	serving := make(chan struct{})
	go func() {
		defer close(serving)
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) {
			t.Errorf("serve: %v", serveErr)
		}
	}()
	client, err := grpc.NewClient(
		"passthrough:///forced-shutdown", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := client.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	response := make(chan error, 1)
	go func() {
		_, callErr := healthpb.NewHealthClient(client).Check(ctx, &healthpb.HealthCheckRequest{})
		response <- callErr
	}()
	waitForShutdownSignal(ctx, t, service.started, "RPC never started")
	shutdown, stop := context.WithCancel(ctx)
	stop()
	stopped := make(chan struct{})
	go func() { grpcserverpkg.GracefulStop(shutdown, server, time.Millisecond); close(stopped) }()
	// The handler cannot drain voluntarily: only forced Stop cancels its context.
	waitForShutdownSignal(ctx, t, stopped, "forced shutdown did not finish")
	waitForShutdownSignal(ctx, t, service.finished, "forced shutdown left handler running")
	select {
	case callErr := <-response:
		if callErr == nil {
			t.Fatal("forced shutdown unexpectedly completed RPC")
		}
	case <-ctx.Done():
		t.Fatal("client RPC did not finish")
	}
	waitForShutdownSignal(ctx, t, serving, "server did not stop serving")
	if err := ctx.Err(); err != nil {
		t.Fatalf("shutdown relied on the outer deadline: %v", err)
	}
}

func waitForShutdownSignal(ctx context.Context, t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatal(failure)
	}
}
