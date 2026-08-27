package container

import (
	"errors"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultEndpointCacheMaxEntries = 256
	defaultEndpointCacheIdleTTL    = 30 * time.Minute
)

type closeable interface {
	Close() error
}

// EndpointCache reuses closeable clients keyed by runtime endpoint.
type EndpointCache[T closeable] struct {
	mu         sync.Mutex
	clients    map[string]endpointCacheEntry[T]
	closed     bool
	newClient  func(endpoint string) (T, error)
	group      singleflight.Group
	maxEntries int
	idleTTL    time.Duration
	now        func() time.Time
}

type endpointCacheEntry[T closeable] struct {
	client   T
	lastUsed time.Time
}

// NewEndpointCache creates a cache for endpoint-scoped clients.
func NewEndpointCache[T closeable](newClient func(endpoint string) (T, error)) *EndpointCache[T] {
	return &EndpointCache[T]{
		clients:    make(map[string]endpointCacheEntry[T]),
		newClient:  newClient,
		maxEntries: defaultEndpointCacheMaxEntries,
		idleTTL:    defaultEndpointCacheIdleTTL,
		now:        time.Now,
	}
}

// Get returns the cached client for endpoint or constructs one on cache miss.
func (c *EndpointCache[T]) Get(endpoint string) (T, error) {
	var zero T
	if endpoint == "" {
		return zero, status.Error(codes.InvalidArgument, "runtime endpoint is required")
	}

	value, err, _ := c.group.Do(endpoint, func() (any, error) {
		now := c.now()
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return zero, status.Error(codes.FailedPrecondition, "endpoint cache is closed")
		}
		evicted := c.evictExpiredLocked(now)
		if entry, ok := c.clients[endpoint]; ok {
			entry.lastUsed = now
			c.clients[endpoint] = entry
			c.mu.Unlock()
			closeEndpointClients(evicted)
			return entry.client, nil
		}
		c.mu.Unlock()
		closeEndpointClients(evicted)

		client, err := c.newClient(endpoint)
		if err != nil {
			return zero, err
		}

		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			_ = client.Close()
			return zero, status.Error(codes.FailedPrecondition, "endpoint cache is closed")
		}
		if existing, ok := c.clients[endpoint]; ok {
			existing.lastUsed = c.now()
			c.clients[endpoint] = existing
			c.mu.Unlock()
			_ = client.Close()
			return existing.client, nil
		}
		evicted = c.evictLRULocked()
		c.clients[endpoint] = endpointCacheEntry[T]{client: client, lastUsed: c.now()}
		c.mu.Unlock()
		closeEndpointClients(evicted)
		return client, nil
	})
	if err != nil {
		return zero, err
	}

	client, ok := value.(T)
	if !ok {
		return zero, status.Error(codes.Internal, "endpoint cache returned unexpected client type")
	}
	return client, nil
}

func (c *EndpointCache[T]) evictExpiredLocked(now time.Time) []T {
	if c.idleTTL <= 0 {
		return nil
	}
	evicted := make([]T, 0)
	for endpoint, entry := range c.clients {
		if now.Sub(entry.lastUsed) >= c.idleTTL {
			delete(c.clients, endpoint)
			evicted = append(evicted, entry.client)
		}
	}
	return evicted
}

func (c *EndpointCache[T]) evictLRULocked() []T {
	if c.maxEntries <= 0 || len(c.clients) < c.maxEntries {
		return nil
	}
	var oldestEndpoint string
	var oldestTime time.Time
	for endpoint, entry := range c.clients {
		if oldestEndpoint == "" || entry.lastUsed.Before(oldestTime) {
			oldestEndpoint = endpoint
			oldestTime = entry.lastUsed
		}
	}
	entry := c.clients[oldestEndpoint]
	delete(c.clients, oldestEndpoint)
	return []T{entry.client}
}

func closeEndpointClients[T closeable](clients []T) {
	for _, client := range clients {
		_ = client.Close()
	}
}

// Close closes all cached clients. It is safe to call more than once.
func (c *EndpointCache[T]) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	clients := make([]T, 0, len(c.clients))
	for _, entry := range c.clients {
		clients = append(clients, entry.client)
	}
	c.clients = make(map[string]endpointCacheEntry[T])
	c.mu.Unlock()

	errs := make([]error, 0, len(clients))
	for _, client := range clients {
		if err := client.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
