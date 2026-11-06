//nolint:testpackage // Configures cache expiry and observes close-error aggregation.
package container

import (
	"errors"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type failingEndpointClient struct {
	err    error
	closed int
}

func (c *failingEndpointClient) Close() error { c.closed++; return c.err }

func TestEndpointCacheCloseAggregatesFailures(t *testing.T) {
	firstErr, secondErr := errors.New("first close"), errors.New("second close")
	clients := map[string]*failingEndpointClient{"first": {err: firstErr}, "second": {err: secondErr}}
	cache := NewEndpointCache(func(endpoint string) (*failingEndpointClient, error) { return clients[endpoint], nil })
	for endpoint := range clients {
		if _, err := cache.Get(endpoint); err != nil {
			t.Fatal(err)
		}
	}
	err := cache.Close()
	if !errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
		t.Fatalf("joined close error = %v", err)
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	for name, client := range clients {
		if client.closed != 1 {
			t.Fatalf("%s closed %d times", name, client.closed)
		}
	}
}

func TestEndpointCacheDisabledExpiryRetainsClients(t *testing.T) {
	now := time.Unix(100, 0)
	cache := NewEndpointCache(func(string) (*failingEndpointClient, error) { return &failingEndpointClient{}, nil })
	cache.idleTTL = 0
	cache.now = func() time.Time { return now }
	defer cache.Close()
	first, err := cache.Get("runtime")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(24 * time.Hour)
	second, err := cache.Get("runtime")
	if err != nil || first != second || first.closed != 0 {
		t.Fatalf("disabled expiry replaced client: %v, closed=%d", err, first.closed)
	}
}

func TestEndpointCacheCloseDuringConstruction(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseFactory := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseFactory()
	client := &failingEndpointClient{}
	cache := NewEndpointCache(func(string) (*failingEndpointClient, error) { close(started); <-release; return client, nil })
	result := make(chan error, 1)
	go func() { _, err := cache.Get("runtime"); result <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("factory did not start")
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	releaseFactory()
	select {
	case err := <-result:
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("in-flight Get = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight construction did not finish")
	}
	if client.closed != 1 {
		t.Fatalf("new client closed %d times", client.closed)
	}
	if value, err := cache.Get("runtime"); value != nil || status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("closed Get = %v %v", value, err)
	}
	if value, err := cache.Get(""); value != nil || status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty Get = %v %v", value, err)
	}
}
