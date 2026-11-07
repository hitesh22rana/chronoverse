package testkit

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	redispkg "github.com/hitesh22rana/chronoverse/internal/pkg/redis"
)

const redisImage = "redis:8.10.2-alpine3.23"

// startRedis starts a Redis container and returns a ready-to-use store.
func startRedis(ctx context.Context, s *suite) (*redispkg.Store, error) {
	return startRedisWith(ctx, s, redisStartup{
		startContainer: func(ctx context.Context) (*tcredis.RedisContainer, error) { return tcredis.Run(ctx, redisImage) },
		connect:        redispkg.New,
	})
}

type redisStartup struct {
	startContainer func(context.Context) (*tcredis.RedisContainer, error)
	connect        func(context.Context, *redispkg.Config) (*redispkg.Store, error)
}

func startRedisWith(ctx context.Context, s *suite, startup redisStartup) (*redispkg.Store, error) {
	ctr, err := startup.startContainer(ctx)
	if ctr != nil {
		s.containers = append(s.containers, ctr)
	}
	if err != nil {
		return nil, fmt.Errorf("start redis container: %w", err)
	}

	host, portNum, err := hostPort(ctx, ctr, "6379/tcp")
	if err != nil {
		return nil, fmt.Errorf("redis: %w", err)
	}

	rdb, err := startup.connect(ctx, &redispkg.Config{
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
		EvictionPolicy:           "volatile-ttl",
		EvictionPolicySampleSize: 5,
		TLSConfig:                &redispkg.TLSConfig{Enabled: false},
	})
	if err != nil {
		return nil, fmt.Errorf("connect redis: %w", err)
	}

	s.redisAddr = net.JoinHostPort(host, strconv.Itoa(portNum))
	return rdb, nil
}
