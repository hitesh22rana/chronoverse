package main

import (
	"context"
	"fmt"
	"net"
	"os"

	"github.com/go-playground/validator/v10"

	"github.com/hitesh22rana/chronoverse/internal/app/auth"
	"github.com/hitesh22rana/chronoverse/internal/config"
	"github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/pat"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	authRepo "github.com/hitesh22rana/chronoverse/internal/repository/auth"
	authSvc "github.com/hitesh22rana/chronoverse/internal/service/auth"
)

const (
	// ExitOk and ExitError are the exit codes.
	ExitOk = iota
	// ExitError is the exit code for errors.
	ExitError

	// ServiceName is the name of the service.
	ServiceName = "auth-service"
)

func main() {
	os.Exit(run())
}

func run() int {
	// Context to cancel the execution
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load the service configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load configuration: %v\n", err)
		return ExitError
	}

	// Initialize logger
	log, err := logger.New(cfg.Environment.Env)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize logger: %v\n", err)
		return ExitError
	}
	defer func() {
		if err = log.Sync(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to flush logger: %v\n", err)
		}
	}()

	// Initialize the redis store
	rdb, err := redis.New(ctx, &redis.Config{
		Host:                     cfg.Redis.Host,
		Port:                     cfg.Redis.Port,
		Password:                 cfg.Redis.Password,
		DB:                       cfg.Redis.DB,
		PoolSize:                 cfg.Redis.PoolSize,
		MinIdleConns:             cfg.Redis.MinIdleConns,
		ReadTimeout:              cfg.Redis.ReadTimeout,
		WriteTimeout:             cfg.Redis.WriteTimeout,
		MaxMemory:                cfg.Redis.MaxMemory,
		EvictionPolicy:           cfg.Redis.EvictionPolicy,
		EvictionPolicySampleSize: cfg.Redis.EvictionPolicySampleSize,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize redis store: %v\n", err)
		return ExitError
	}
	defer rdb.Close()

	// Initialize the postgres store
	pdb, err := postgres.New(ctx, &postgres.Config{
		Host:        cfg.Postgres.Host,
		Port:        cfg.Postgres.Port,
		User:        cfg.Postgres.User,
		Password:    cfg.Postgres.Password,
		Database:    cfg.Postgres.Database,
		MaxConns:    cfg.Postgres.MaxConns,
		MinConns:    cfg.Postgres.MinConns,
		MaxConnLife: cfg.Postgres.MaxConnLife,
		MaxConnIdle: cfg.Postgres.MaxConnIdle,
		DialTimeout: cfg.Postgres.DialTimeout,
		SSLMode:     cfg.Postgres.SSLMode,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize postgres store: %v\n", err)
		return ExitError
	}
	defer pdb.Close()

	// Initialize the token issuer
	tokenIssuer := pat.New(&pat.Options{
		Expiry: cfg.Pat.DefaultExpiry,
	}, rdb)

	// Initialize the auth repository
	repo := authRepo.New(tokenIssuer, pdb)

	// Initialize the validator utility
	validator := validator.New()

	// Initialize the auth service
	svc := authSvc.New(validator, repo)

	// Initialize the auth app
	app := auth.New(log, svc)

	// Create TCP listener
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.AuthServer.Port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create listener: %v\n", err)
		return ExitError
	}

	// Start the gRPC server
	if err := app.Serve(listener); err != nil {
		fmt.Fprintf(os.Stderr, "failed to start gRPC server: %v\n", err)
		return ExitError
	}

	return ExitOk
}
