package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// MaxHealthCheckRetries is the maximum number of retries for the health check
	MaxHealthCheckRetries = 3
)

// Config is the configuration for the Redis store
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

// RedisStore is a Redis store
type RedisStore struct {
	client *redis.Client
}

// healthCheck is used to check the health of the Redis connection
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

// New creates a new Redis store instance
func New(ctx context.Context, cfg *Config) (*RedisStore, error) {
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
		return nil, fmt.Errorf("failed to set max memory: %v", err)
	}

	// Set the eviction policy
	if err := client.ConfigSet(ctx, "maxmemory-policy", cfg.EvictionPolicy).Err(); err != nil {
		return nil, fmt.Errorf("failed to set eviction policy: %v", err)
	}

	// Set sample size for eviction policy
	if err := client.ConfigSet(ctx, "maxmemory-samples", fmt.Sprintf("%d", cfg.EvictionPolicySampleSize)).Err(); err != nil {
		return nil, fmt.Errorf("failed to set eviction policy sample size (maxmemory-samples): %v", err)
	}

	if err := healthCheck(ctx, client); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %v", err)
	}

	return &RedisStore{client: client}, nil
}

// Close closes the Redis store
func (rs *RedisStore) Close() error {
	return rs.client.Close()
}

// Set stores a value with optional expiration
func (rs *RedisStore) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal value: %v", err)
	}

	if err := rs.client.Set(ctx, key, data, expiration).Err(); err != nil {
		return fmt.Errorf("failed to set value in redis: %v", err)
	}

	return nil
}

// Get retrieves a value by key
func (rs *RedisStore) Get(ctx context.Context, key string, dest interface{}) error {
	data, err := rs.client.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return fmt.Errorf("key not found: %s", key)
		}
		return fmt.Errorf("failed to get value from redis: %v", err)
	}

	if err := json.Unmarshal(data, dest); err != nil {
		return fmt.Errorf("failed to unmarshal value: %v", err)
	}

	return nil
}

// Delete removes a key
func (rs *RedisStore) Delete(ctx context.Context, key string) error {
	if err := rs.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("failed to delete key: %v", err)
	}
	return nil
}

// Exists checks if a key exists
func (rs *RedisStore) Exists(ctx context.Context, key string) (bool, error) {
	result, err := rs.client.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("failed to check if key exists: %v", err)
	}
	return result > 0, nil
}

// SetNX sets a value if it does not exist (atomic operation)
func (rs *RedisStore) SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) (bool, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return false, fmt.Errorf("failed to marshal value: %v", err)
	}

	success, err := rs.client.SetNX(ctx, key, data, expiration).Result()
	if err != nil {
		return false, fmt.Errorf("failed to set value in redis: %v", err)
	}

	return success, nil
}

// Pipeline executes multiple commands in a pipeline
func (rs *RedisStore) Pipeline(ctx context.Context, fn func(redis.Pipeliner) error) error {
	pipe := rs.client.Pipeline()
	if err := fn(pipe); err != nil {
		return fmt.Errorf("failed to execute pipeline function: %v", err)
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to execute pipeline: %v", err)
	}

	return nil
}
