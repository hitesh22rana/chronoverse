package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

	return err
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
		return nil, status.Errorf(codes.Internal, "failed to set max memory: %v", err)
	}

	// Set the eviction policy
	if err := client.ConfigSet(ctx, "maxmemory-policy", cfg.EvictionPolicy).Err(); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to set eviction policy: %v", err)
	}

	// Set sample size for eviction policy
	if err := client.ConfigSet(ctx, "maxmemory-samples", fmt.Sprintf("%d", cfg.EvictionPolicySampleSize)).Err(); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to set eviction policy sample size: %v", err)
	}

	if err := healthCheck(ctx, client); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to connect to Redis: %v", err)
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
		return status.Errorf(codes.Internal, "failed to marshal value: %v", err)
	}

	if err := s.client.Set(ctx, key, data, expiration).Err(); err != nil {
		return status.Errorf(codes.Internal, "failed to set value in redis: %v", err)
	}

	return nil
}

// Get retrieves a value by key.
func (s *Store) Get(ctx context.Context, key string, dest interface{}) error {
	data, err := s.client.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return status.Errorf(codes.NotFound, "key not found: %s", key)
		}
		return status.Errorf(codes.Internal, "failed to get value from redis: %v", err)
	}

	if err := json.Unmarshal(data, dest); err != nil {
		return status.Errorf(codes.Internal, "failed to unmarshal value: %v", err)
	}

	return nil
}

// Delete removes a key.
func (s *Store) Delete(ctx context.Context, key string) error {
	val, err := s.client.Del(ctx, key).Result()
	if err != nil {
		return status.Errorf(codes.Internal, "failed to delete key: %v", err)
	}

	if val == 0 {
		return status.Errorf(codes.NotFound, "key not found: %s", key)
	}

	return nil
}

// Exists checks if a key exists.
func (s *Store) Exists(ctx context.Context, key string) (bool, error) {
	result, err := s.client.Exists(ctx, key).Result()
	if err != nil {
		return false, status.Errorf(codes.Internal, "failed to check if key exists: %v", err)
	}
	return result > 0, nil
}

// SetNX sets a value if it does not exist (atomic operation).
func (s *Store) SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) (bool, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return false, status.Errorf(codes.Internal, "failed to marshal value: %v", err)
	}

	success, err := s.client.SetNX(ctx, key, data, expiration).Result()
	if err != nil {
		return false, status.Errorf(codes.Internal, "failed to set value in redis: %v", err)
	}

	return success, nil
}

// Pipeline executes multiple commands in a pipeline.
func (s *Store) Pipeline(ctx context.Context, fn func(redis.Pipeliner) error) error {
	pipe := s.client.Pipeline()
	if err := fn(pipe); err != nil {
		return status.Errorf(codes.Internal, "failed to execute pipeline: %v", err)
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to execute pipeline: %v", err)
	}

	return nil
}
