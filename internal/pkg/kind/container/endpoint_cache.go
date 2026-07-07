package container

import (
	"errors"
	"sync"

	"golang.org/x/sync/singleflight"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type closeable interface {
	Close() error
}

// EndpointCache reuses closeable clients keyed by runtime endpoint.
type EndpointCache[T closeable] struct {
	mu        sync.Mutex
	clients   map[string]T
	closed    bool
	newClient func(endpoint string) (T, error)
	group     singleflight.Group
}

// NewEndpointCache creates a cache for endpoint-scoped clients.
func NewEndpointCache[T closeable](newClient func(endpoint string) (T, error)) *EndpointCache[T] {
	return &EndpointCache[T]{
		clients:   make(map[string]T),
		newClient: newClient,
	}
}

// Get returns the cached client for endpoint or constructs one on cache miss.
func (c *EndpointCache[T]) Get(endpoint string) (T, error) {
	var zero T
	if endpoint == "" {
		return zero, status.Error(codes.InvalidArgument, "runtime endpoint is required")
	}

	value, err, _ := c.group.Do(endpoint, func() (any, error) {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return zero, status.Error(codes.FailedPrecondition, "endpoint cache is closed")
		}
		if client, ok := c.clients[endpoint]; ok {
			c.mu.Unlock()
			return client, nil
		}
		c.mu.Unlock()

		client, err := c.newClient(endpoint)
		if err != nil {
			return zero, err
		}

		c.mu.Lock()
		defer c.mu.Unlock()
		if c.closed {
			_ = client.Close()
			return zero, status.Error(codes.FailedPrecondition, "endpoint cache is closed")
		}
		if existing, ok := c.clients[endpoint]; ok {
			_ = client.Close()
			return existing, nil
		}
		c.clients[endpoint] = client
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

// Close closes all cached clients. It is safe to call more than once.
func (c *EndpointCache[T]) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	clients := make([]T, 0, len(c.clients))
	for _, client := range c.clients {
		clients = append(clients, client)
	}
	c.clients = make(map[string]T)
	c.mu.Unlock()

	errs := make([]error, 0, len(clients))
	for _, client := range clients {
		if err := client.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
