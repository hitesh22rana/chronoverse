package main

import (
	"os"

	"github.com/hitesh22rana/chronoverse/internal/config"
	"github.com/hitesh22rana/chronoverse/internal/redis"
)

const (
	// ExitOk and ExitError are the exit codes
	ExitOk = iota
	// ExitError is the exit code for errors
	ExitError

	// ServiceName is the name of the service
	ServiceName = "authority-service"
)

func main() {
	os.Exit(run())
}

func run() int {
	// Load the service configuration
	cfg, err := config.Load()
	if err != nil {
		return ExitError
	}

	// Initialize the redis store
	_, close, err := redis.NewRedisStore(&redis.RedisConfig{
		Host:                     cfg.Redis.Host,
		Port:                     cfg.Redis.Port,
		Password:                 cfg.Redis.Password,
		DB:                       cfg.Redis.DB,
		MaxMemory:                cfg.Redis.MaxMemory,
		EvictionPolicy:           cfg.Redis.EvictionPolicy,
		EvictionPolicySampleSize: cfg.Redis.EvictionPolicySampleSize,
	})
	if err != nil {
		return ExitError
	}
	defer close()

	return ExitOk
}
