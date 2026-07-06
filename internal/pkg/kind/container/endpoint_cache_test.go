package container_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type testEndpointClient struct {
	endpoint   string
	closeCalls atomic.Int32
}

func (c *testEndpointClient) Close() error {
	c.closeCalls.Add(1)
	return nil
}

func TestEndpointCacheReusesClientForEndpoint(t *testing.T) {
	t.Parallel()

	var constructions atomic.Int32
	cache := container.NewEndpointCache(func(endpoint string) (*testEndpointClient, error) {
		constructions.Add(1)
		return &testEndpointClient{endpoint: endpoint}, nil
	})

	first, err := cache.Get("tcp://runtime-a:2375")
	if err != nil {
		t.Fatalf("Get() first error = %v", err)
	}
	second, err := cache.Get("tcp://runtime-a:2375")
	if err != nil {
		t.Fatalf("Get() second error = %v", err)
	}
	if first != second {
		t.Fatal("expected same endpoint to reuse cached client")
	}
	if got := constructions.Load(); got != 1 {
		t.Fatalf("constructions = %d, want 1", got)
	}
}

func TestEndpointCacheUsesSeparateClientsForDifferentEndpoints(t *testing.T) {
	t.Parallel()

	cache := container.NewEndpointCache(func(endpoint string) (*testEndpointClient, error) {
		return &testEndpointClient{endpoint: endpoint}, nil
	})

	first, err := cache.Get("tcp://runtime-a:2375")
	if err != nil {
		t.Fatalf("Get() first error = %v", err)
	}
	second, err := cache.Get("tcp://runtime-b:2375")
	if err != nil {
		t.Fatalf("Get() second error = %v", err)
	}
	if first == second {
		t.Fatal("expected different endpoints to use different clients")
	}
}

func TestEndpointCacheDoesNotCacheConstructionErrors(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("dial failed")
	var constructions atomic.Int32
	cache := container.NewEndpointCache(func(endpoint string) (*testEndpointClient, error) {
		if constructions.Add(1) == 1 {
			return nil, wantErr
		}
		return &testEndpointClient{endpoint: endpoint}, nil
	})

	if _, err := cache.Get("tcp://runtime-a:2375"); !errors.Is(err, wantErr) {
		t.Fatalf("Get() first error = %v, want %v", err, wantErr)
	}
	if _, err := cache.Get("tcp://runtime-a:2375"); err != nil {
		t.Fatalf("Get() second error = %v", err)
	}
	if got := constructions.Load(); got != 2 {
		t.Fatalf("constructions = %d, want 2", got)
	}
}

func TestEndpointCacheDeduplicatesConcurrentConstruction(t *testing.T) {
	t.Parallel()

	var constructions atomic.Int32
	cache := container.NewEndpointCache(func(endpoint string) (*testEndpointClient, error) {
		constructions.Add(1)
		return &testEndpointClient{endpoint: endpoint}, nil
	})

	const workers = 32
	start := make(chan struct{})
	results := make(chan *testEndpointClient, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			client, err := cache.Get("tcp://runtime-a:2375")
			if err != nil {
				errs <- err
				return
			}
			results <- client
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Fatalf("Get() error = %v", err)
	}
	var first *testEndpointClient
	for client := range results {
		if first == nil {
			first = client
			continue
		}
		if client != first {
			t.Fatal("expected concurrent calls to share one cached client")
		}
	}
	if got := constructions.Load(); got != 1 {
		t.Fatalf("constructions = %d, want 1", got)
	}
}

func TestEndpointCacheCloseClosesCachedClientsOnce(t *testing.T) {
	t.Parallel()

	cache := container.NewEndpointCache(func(endpoint string) (*testEndpointClient, error) {
		return &testEndpointClient{endpoint: endpoint}, nil
	})
	first, err := cache.Get("tcp://runtime-a:2375")
	if err != nil {
		t.Fatalf("Get() first error = %v", err)
	}
	second, err := cache.Get("tcp://runtime-b:2375")
	if err != nil {
		t.Fatalf("Get() second error = %v", err)
	}

	if err := cache.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := cache.Close(); err != nil {
		t.Fatalf("Close() second error = %v", err)
	}
	if got := first.closeCalls.Load(); got != 1 {
		t.Fatalf("first close calls = %d, want 1", got)
	}
	if got := second.closeCalls.Load(); got != 1 {
		t.Fatalf("second close calls = %d, want 1", got)
	}
	if _, err := cache.Get("tcp://runtime-c:2375"); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("Get() after Close() code = %s, want %s: %v", status.Code(err), codes.FailedPrecondition, err)
	}
}

func TestEndpointCacheCloseToleratesEmptyCache(t *testing.T) {
	t.Parallel()

	cache := container.NewEndpointCache(func(endpoint string) (*testEndpointClient, error) {
		return &testEndpointClient{endpoint: endpoint}, nil
	})
	if err := cache.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
