package main

import (
	"context"
	"fmt"
	"net"
	"os"

	"github.com/go-playground/validator/v10"
	"go.uber.org/zap"

	"github.com/hitesh22rana/chronoverse/internal/app/users"
	"github.com/hitesh22rana/chronoverse/internal/config"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	otelpkg "github.com/hitesh22rana/chronoverse/internal/pkg/otel"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
	usersrepo "github.com/hitesh22rana/chronoverse/internal/repository/users"
	userssvc "github.com/hitesh22rana/chronoverse/internal/service/users"
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

	// authPrivateKeyPath is the path to the private key.
	authPrivateKeyPath string

	// authPublicKeyPath is the path to the public key.
	authPublicKeyPath string
)

func main() {
	os.Exit(run())
}

func run() int {
	// Global context to cancel the execution
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize the service information
	initSvcInfo()

	// Load the users service configuration
	cfg, err := config.InitUsersServiceConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	// Initialize the OpenTelemetry Resource
	res, err := otelpkg.InitResource(ctx, svcpkg.Info().GetName(), svcpkg.Info().GetVersion())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	// Initialize the OpenTelemetry TracerProvider
	tp, err := otelpkg.InitTracerProvider(ctx, res)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer func() {
		if err = tp.Shutdown(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "failed to shutdown tracer provider: %v\n", err)
		}
	}()

	// Initialize the OpenTelemetry MeterProvider
	mp, err := otelpkg.InitMeterProvider(ctx, res)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer func() {
		if err = mp.Shutdown(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "failed to shutdown meter provider: %v\n", err)
		}
	}()

	// Initialize the OpenTelemetry LoggerProvider
	lp, err := otelpkg.InitLogProvider(ctx, res)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer func() {
		if err = lp.Shutdown(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "failed to shutdown logger provider: %v\n", err)
		}
	}()

	// Initialize and set the logger in the context
	ctx, logger := loggerpkg.Init(ctx, svcpkg.Info().GetName(), lp)
	defer func() {
		if err = logger.Sync(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to sync logger: %v\n", err)
		}
	}()

	// Initialize the auth issuer
	auth, err := auth.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

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
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer pdb.Close()

	// Initialize the users repository
	repo := usersrepo.New(auth, pdb)

	// Initialize the validator utility
	validator := validator.New()

	// Initialize the users service
	svc := userssvc.New(validator, repo)

	// Initialize the users application
	app := users.New(ctx, &users.Config{
		Deadline:    cfg.Grpc.RequestTimeout,
		Environment: cfg.Environment.Env,
	}, svc)

	// Create a TCP listener
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Grpc.Port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create listener: %v\n", err)
		return ExitError
	}

	// Graceful shutdown the service
	go func() {
		<-ctx.Done()
		if err := listener.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to close listener: %v\n", err)
		}
	}()

	// Log the service information
	logger.Info(
		"starting service",
		zap.Any("ctx", ctx),
		zap.String("name", svcpkg.Info().Name),
		zap.String("version", svcpkg.Info().Version),
		zap.String("address", listener.Addr().String()),
		zap.String("environment", cfg.Environment.Env),
	)

	// Start the gRPC server
	if err := app.Serve(listener); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	return ExitOk
}

// initSvcInfo initializes the service information.
func initSvcInfo() {
	svcpkg.SetVersion(version)
	svcpkg.SetName(name)
	svcpkg.SetAuthPrivateKeyPath(authPrivateKeyPath)
	svcpkg.SetAuthPublicKeyPath(authPublicKeyPath)
}
