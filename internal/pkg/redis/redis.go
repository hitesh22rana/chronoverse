package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/extra/redisotel/v9"
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

	// Check the health of the connection
	if err := healthCheck(ctx, client); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to connect to Redis: %v", err)
	}

	// Enable tracing instrumentation for Redis
	if err := redisotel.InstrumentTracing(client); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to instrument tracing: %v", err)
	}

	// Enable metrics instrumentation for Redis
	if err := redisotel.InstrumentMetrics(client); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to instrument metrics: %v", err)
	}

	return &Store{client: client}, nil
}

// Close closes the Redis store.
func (s *Store) Close() error {
	return s.client.Close()
}

// Set stores a value with optional expiration.
func (s *Store) Set(ctx context.Context, key string, value any, expiration time.Duration) error {
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
func (s *Store) Get(ctx context.Context, key string, dest any) (any, error) {
	data, err := s.client.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, status.Errorf(codes.NotFound, "key not found: %s", key)
		}
		return nil, status.Errorf(codes.Internal, "failed to get value from redis: %v", err)
	}

	if err := json.Unmarshal(data, dest); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to unmarshal value: %v", err)
	}

	return dest, nil
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

// DeleteByPattern deletes all keys that match the given pattern.
func (s *Store) DeleteByPattern(ctx context.Context, pattern string) (int64, error) {
	var cursor uint64
	var totalDeleted int64

	for {
		var keys []string
		var err error

		// Scan for keys matching pattern (safer than KEYS command)
		keys, cursor, err = s.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return 0, status.Errorf(codes.Internal, "failed to scan keys: %v", err)
		}

		// If keys found, delete them in a pipeline for efficiency
		if len(keys) > 0 {
			pipe := s.client.Pipeline()
			for _, key := range keys {
				pipe.Del(ctx, key)
			}

			cmds, err := pipe.Exec(ctx)
			if err != nil {
				return 0, status.Errorf(codes.Internal, "failed to delete keys: %v", err)
			}

			// Count deleted keys
			for _, cmd := range cmds {
				if delCmd, ok := cmd.(*redis.IntCmd); ok {
					totalDeleted += delCmd.Val()
				}
			}
		}

		// Stop if cursor is 0 (no more keys to scan)
		if cursor == 0 {
			break
		}
	}

	return totalDeleted, nil
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
func (s *Store) SetNX(ctx context.Context, key string, value any, expiration time.Duration) (bool, error) {
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

// Publish publishes a message to a channel.
func (s *Store) Publish(ctx context.Context, channel string, message any) error {
	if err := s.client.Publish(ctx, channel, message).Err(); err != nil {
		return status.Errorf(codes.Internal, "failed to publish message to channel %s: %v", channel, err)
	}

	return nil
}

// Subscribe subscribes to specified channels and returns a PubSub client.
// The caller is responsible for closing the PubSub client.
func (s *Store) Subscribe(ctx context.Context, channels ...string) *redis.PubSub {
	return s.client.Subscribe(ctx, channels...)
}
