//nolint:testpackage // Configures the cache clock and bounds for deterministic eviction tests.
package container

import (
	"sync/atomic"
	"testing"
	"time"
)

type evictionTestClient struct {
	closeCalls atomic.Int32
}

func (c *evictionTestClient) Close() error {
	c.closeCalls.Add(1)
	return nil
}

func TestEndpointCacheEvictsIdleClients(t *testing.T) {
	now := time.Unix(1_000, 0)
	cache := NewEndpointCache(func(string) (*evictionTestClient, error) {
		return &evictionTestClient{}, nil
	})
	cache.idleTTL = time.Minute
	cache.now = func() time.Time { return now }

	first, err := cache.Get("tcp://runtime-a:2376")
	if err != nil {
		t.Fatalf("Get() first error = %v", err)
	}
	now = now.Add(time.Minute)
	if _, err := cache.Get("tcp://runtime-b:2376"); err != nil {
		t.Fatalf("Get() second error = %v", err)
	}
	if got := first.closeCalls.Load(); got != 1 {
		t.Fatalf("expired client close calls = %d, want 1", got)
	}
}

func TestEndpointCacheEvictsLeastRecentlyUsedClientAtCapacity(t *testing.T) {
	now := time.Unix(1_000, 0)
	cache := NewEndpointCache(func(string) (*evictionTestClient, error) {
		return &evictionTestClient{}, nil
	})
	cache.maxEntries = 2
	cache.idleTTL = time.Hour
	cache.now = func() time.Time { return now }

	first, err := cache.Get("tcp://runtime-a:2376")
	if err != nil {
		t.Fatalf("Get() first error = %v", err)
	}
	now = now.Add(time.Second)
	if _, err := cache.Get("tcp://runtime-b:2376"); err != nil {
		t.Fatalf("Get() second error = %v", err)
	}
	now = now.Add(time.Second)
	if _, err := cache.Get("tcp://runtime-c:2376"); err != nil {
		t.Fatalf("Get() third error = %v", err)
	}
	if got := first.closeCalls.Load(); got != 1 {
		t.Fatalf("least-recent client close calls = %d, want 1", got)
	}
}
