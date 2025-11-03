package main

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"runtime/debug"

	_ "github.com/KimMachineGun/automemlimit"
	"github.com/go-playground/validator/v10"
	_ "go.uber.org/automaxprocs"
	"go.uber.org/zap"

	"github.com/hitesh22rana/chronoverse/internal/app/workflows"
	"github.com/hitesh22rana/chronoverse/internal/config"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
	workflowsrepo "github.com/hitesh22rana/chronoverse/internal/repository/workflows"
	workflowssvc "github.com/hitesh22rana/chronoverse/internal/service/workflows"
)

const (
	// ExitOk and ExitError are the exit codes.
	ExitOk = iota
	// ExitError is the exit code for errors.
	ExitError
)

func main() {
	os.Exit(run())
}

func run() int {
	// Initialize the service with, all necessary components
	ctx, cancel := svcpkg.Init()
	defer cancel()

	// Load the workflows service configuration
	cfg, err := config.InitWorkflowsServiceConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	// Initialize the auth issuer
	auth, err := auth.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	// Initialize the PostgreSQL database
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
		TLSConfig: &postgres.TLSConfig{
			Enabled:  cfg.Postgres.TLS.Enabled,
			CAFile:   cfg.Postgres.TLS.CAFile,
			CertFile: cfg.Postgres.TLS.CertFile,
			KeyFile:  cfg.Postgres.TLS.KeyFile,
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer pdb.Close()

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
		TLSConfig: &redis.TLSConfig{
			Enabled:  cfg.Redis.TLS.Enabled,
			CAFile:   cfg.Redis.TLS.CAFile,
			CertFile: cfg.Redis.TLS.CertFile,
			KeyFile:  cfg.Redis.TLS.KeyFile,
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer rdb.Close()

	// Initialize the kafka client
	kfk, err := kafka.New(ctx,
		kafka.WithBrokers(cfg.Kafka.Brokers...),
		kafka.WithTLS(&cfg.Kafka),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer kfk.Close()

	// Initialize the workflows repository
	repo := workflowsrepo.New(&workflowsrepo.Config{
		FetchLimit: cfg.WorkflowsServiceConfig.FetchLimit,
	}, pdb, kfk)

	// Initialize the validator utility
	validator := validator.New()

	// Initialize the workflows service
	svc := workflowssvc.New(validator, repo, rdb)

	// Initialize the workflows application
	app := workflows.New(ctx, &workflows.Config{
		Deadline:    cfg.Grpc.RequestTimeout,
		Environment: cfg.Environment.Env,
		TLSConfig: &workflows.TLSConfig{
			Enabled:  cfg.Grpc.TLS.Enabled,
			CAFile:   cfg.Grpc.TLS.CAFile,
			CertFile: cfg.Grpc.TLS.CertFile,
			KeyFile:  cfg.Grpc.TLS.KeyFile,
		},
	}, auth, svc)

	// Create a TCP listener
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Grpc.Port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create listener: %v\n", err)
		return ExitError
	}

	// Gracefully shutdown the service
	go func() {
		<-ctx.Done()
		if err := listener.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to close listener: %v\n", err)
		}
	}()

	// Log the service information
	loggerpkg.FromContext(ctx).Info(
		"starting service",
		zap.Any("ctx", ctx),
		zap.String("name", svcpkg.Info().GetName()),
		zap.String("version", svcpkg.Info().GetVersion()),
		zap.String("address", listener.Addr().String()),
		zap.String("environment", cfg.Environment.Env),
		zap.Bool("tls_enabled", cfg.Grpc.TLS.Enabled),
		zap.Int("gomaxprocs", runtime.GOMAXPROCS(0)),
		zap.Int64("gomemlimit", debug.SetMemoryLimit(0)),
	)

	// Serve the gRPC service
	if err := app.Serve(listener); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	return ExitOk
}
