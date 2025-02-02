package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// MaxHealthCheckRetries is the maximum number of retries for the health check.
	MaxHealthCheckRetries = 3
)

// Config is the configuration for the Redis store.
type Config struct {
	Host                     string
	Port                     int
	Password                 string
	DB                       int
	PoolSize                 int
	MinIdleConns             int
	ReadTimeout              time.Duration
	WriteTimeout             time.Duration
	MaxMemory                string
	EvictionPolicy           string
	EvictionPolicySampleSize int
}

// Store is a Redis store.
type Store struct {
	client *redis.Client
}

// healthCheck is used to check the health of the Redis connection.
func healthCheck(ctx context.Context, client *redis.Client) error {
	var err error

	backoff := 100 * time.Millisecond
	for i := 1; i <= MaxHealthCheckRetries; i++ {
		if err = client.Ping(ctx).Err(); err == nil {
			break
		}
		if i < MaxHealthCheckRetries {
			time.Sleep(backoff)
			backoff *= 2
		}
	}

	if err != nil {
		return fmt.Errorf("%w", err)
	}

	return nil
}

// New creates a new Redis store instance.
func New(ctx context.Context, cfg *Config) (*Store, error) {
	client := redis.NewClient(&redis.Options{
		Addr:         fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     cfg.PoolSize,
		MinIdleConns: cfg.MinIdleConns,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	})

	// Set the max memory
	if err := client.ConfigSet(ctx, "maxmemory", cfg.MaxMemory).Err(); err != nil {
		return nil, fmt.Errorf("failed to set max memory: %w", err)
	}

	// Set the eviction policy
	if err := client.ConfigSet(ctx, "maxmemory-policy", cfg.EvictionPolicy).Err(); err != nil {
		return nil, fmt.Errorf("failed to set eviction policy: %w", err)
	}

	// Set sample size for eviction policy
	if err := client.ConfigSet(ctx, "maxmemory-samples", fmt.Sprintf("%d", cfg.EvictionPolicySampleSize)).Err(); err != nil {
		return nil, fmt.Errorf("failed to set eviction policy sample size (maxmemory-samples): %w", err)
	}

	if err := healthCheck(ctx, client); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	return &Store{client: client}, nil
}

// Close closes the Redis store.
func (s *Store) Close() error {
	return s.client.Close()
}

// Set stores a value with optional expiration.
func (s *Store) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal value: %w", err)
	}

	if err := s.client.Set(ctx, key, data, expiration).Err(); err != nil {
		return fmt.Errorf("failed to set value in redis: %w", err)
	}

	return nil
}

// Get retrieves a value by key.
func (s *Store) Get(ctx context.Context, key string, dest interface{}) error {
	data, err := s.client.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return fmt.Errorf("key not found: %s", key)
		}
		return fmt.Errorf("failed to get value from redis: %w", err)
	}

	if err := json.Unmarshal(data, dest); err != nil {
		return fmt.Errorf("failed to unmarshal value: %w", err)
	}

	return nil
}

// Delete removes a key.
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := s.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("failed to delete key: %w", err)
	}
	return nil
}

// Exists checks if a key exists.
func (s *Store) Exists(ctx context.Context, key string) (bool, error) {
	result, err := s.client.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("failed to check if key exists: %w", err)
	}
	return result > 0, nil
}

// SetNX sets a value if it does not exist (atomic operation).
func (s *Store) SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) (bool, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return false, fmt.Errorf("failed to marshal value: %w", err)
	}

	success, err := s.client.SetNX(ctx, key, data, expiration).Result()
	if err != nil {
		return false, fmt.Errorf("failed to set value in redis: %w", err)
	}

	return success, nil
}

// Pipeline executes multiple commands in a pipeline.
func (s *Store) Pipeline(ctx context.Context, fn func(redis.Pipeliner) error) error {
	pipe := s.client.Pipeline()
	if err := fn(pipe); err != nil {
		return fmt.Errorf("failed to execute pipeline function: %w", err)
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to execute pipeline: %w", err)
	}

	return nil
}
