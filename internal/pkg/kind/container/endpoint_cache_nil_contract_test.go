package container_test

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
)

type endpointClient interface{ Close() error }

func TestEndpointCacheRejectsNilInterfaceFactoryResult(t *testing.T) {
	cache := container.NewEndpointCache(func(string) (endpointClient, error) {
		//nolint:nilnil // Reproduces an invalid public factory result that the cache must reject.
		return nil, nil
	})
	client, err := cache.Get("runtime")
	if client != nil || status.Code(err) != codes.Internal {
		t.Fatalf("nil factory result = %v, %v", client, err)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("Close after rejected factory result panicked: %v", recovered)
		}
	}()
	if err := cache.Close(); err != nil {
		t.Fatalf("Close after rejected factory result = %v", err)
	}
}

type nilContractClient struct{ closes int }

func (c *nilContractClient) Close() error { c.closes++; return nil }

func TestEndpointCacheRetriesRejectedNilFactoryResult(t *testing.T) {
	calls := 0
	want := &nilContractClient{}
	cache := container.NewEndpointCache(func(string) (endpointClient, error) {
		calls++
		if calls == 1 {
			//nolint:nilnil // A rejected factory result must allow a valid retry.
			return nil, nil
		}
		return want, nil
	})
	if client, err := cache.Get("runtime"); client != nil || status.Code(err) != codes.Internal {
		t.Fatalf("nil factory result = %v, %v", client, err)
	}
	for range 2 {
		if client, err := cache.Get("runtime"); client != want || err != nil {
			t.Fatalf("retry factory result = %v, %v", client, err)
		}
	}
	if calls != 2 {
		t.Fatalf("factory calls = %d, want 2", calls)
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	if want.closes != 1 {
		t.Fatalf("valid client closed %d times", want.closes)
	}
}
