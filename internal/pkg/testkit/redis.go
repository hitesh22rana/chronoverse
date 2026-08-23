package testkit

import (
	"context"
	"fmt"
	"time"

	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	redispkg "github.com/hitesh22rana/chronoverse/internal/pkg/redis"
)

const redisImage = "redis:8.2.1-alpine"

// startRedis starts a Redis container and returns a ready-to-use store.
func startRedis(ctx context.Context, s *suite) (*redispkg.Store, error) {
	ctr, err := tcredis.Run(ctx, redisImage)
	if err != nil {
		return nil, fmt.Errorf("start redis container: %w", err)
	}
	s.containers = append(s.containers, ctr)

	host, portNum, err := hostPort(ctx, ctr, "6379/tcp")
	if err != nil {
		return nil, fmt.Errorf("redis: %w", err)
	}

	rdb, err := redispkg.New(ctx, &redispkg.Config{
		Host:         host,
		Port:         portNum,
		DB:           0,
		PoolSize:     10,
		MinIdleConns: 2,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		// Mirror the production defaults; an empty MaxMemory makes
		// CONFIG SET maxmemory fail on Redis 8.
		MaxMemory:                "100mb",
		EvictionPolicy:           "allkeys-lru",
		EvictionPolicySampleSize: 5,
		TLSConfig:                &redispkg.TLSConfig{Enabled: false},
	})
	if err != nil {
		return nil, fmt.Errorf("connect redis: %w", err)
	}

	return rdb, nil
}
