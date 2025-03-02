package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"github.com/hitesh22rana/chronoverse/internal/app/scheduler"
	"github.com/hitesh22rana/chronoverse/internal/config"
	"github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/otel"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
	schedulerrepo "github.com/hitesh22rana/chronoverse/internal/repository/scheduler"
	schedulersvc "github.com/hitesh22rana/chronoverse/internal/service/scheduler"
)

const (
	// ExitOk and ExitError are the exit codes.
	ExitOk = iota
	// ExitError is the exit code for errors.
	ExitError
)

var (
	// version is the service version.
	version string

	// name is the name of the service.
	name string
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize the service information
	initSvcInfo()

	// Handle OS signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
	}()

	// Load the jobs service configuration
	cfg, err := config.InitSchedulerConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	// Initialize OTel Resource
	res, err := otel.InitResource(ctx, svcpkg.Info().GetName(), svcpkg.Info().GetVersion())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to init OTel resource: %v\n", err)
		return ExitError
	}

	// Initialize TracerProvider
	tp, err := otel.InitTracerProvider(ctx, res)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to init tracer provider: %v\n", err)
		return ExitError
	}
	defer func() {
		if err = tp.Shutdown(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to shutdown tracer provider: %v\n", err)
		}
	}()

	// Initialize MeterProvider (optional for metrics)
	mp, err := otel.InitMeterProvider(ctx, res)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to init meter provider: %v\n", err)
		return ExitError
	}
	defer func() {
		if err = mp.Shutdown(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to shutdown meter provider: %v\n", err)
		}
	}()

	// Initialize LoggerProvider
	lp, err := otel.InitLogProvider(ctx, res)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to init log provider: %v\n", err)
		return ExitError
	}
	defer func() {
		if err = lp.Shutdown(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to shutdown log provider: %v\n", err)
		}
	}()

	// Set up logger
	ctx, logger := logger.Init(ctx, svcpkg.Info().GetName(), lp)
	defer func() {
		if err = logger.Sync(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to sync logger: %v\n", err)
		}
	}()

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
		SSLMode:     cfg.Postgres.SSLMode,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer pdb.Close()

	// Initialize scheduler components
	repo := schedulerrepo.New(pdb)
	svc := schedulersvc.New(repo)
	app := scheduler.New(ctx, &scheduler.Config{
		PollInterval: cfg.Scheduler.PollInterval,
	}, svc)

	// Log the service information
	logger.Info(
		"starting service",
		zap.Any("ctx", ctx),
		zap.String("name", svcpkg.Info().Name),
		zap.String("version", svcpkg.Info().Version),
		zap.String("environment", cfg.Environment.Env),
	)

	// Run the scheduler
	if err := app.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	return ExitOk
}

// initSvcInfo initializes the service information.
func initSvcInfo() {
	svcpkg.SetVersion(version)
	svcpkg.SetName(name)
}
