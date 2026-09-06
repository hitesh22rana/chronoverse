package main

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"runtime/debug"
	"time"

	_ "github.com/KimMachineGun/automemlimit"
	"github.com/go-playground/validator/v10"
	_ "go.uber.org/automaxprocs"
	"go.uber.org/zap"

	"github.com/hitesh22rana/chronoverse/internal/app/analytics"
	"github.com/hitesh22rana/chronoverse/internal/config"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	grpcserverpkg "github.com/hitesh22rana/chronoverse/internal/pkg/grpcserver"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
	analyticsrepo "github.com/hitesh22rana/chronoverse/internal/repository/analytics"
	analyticssvc "github.com/hitesh22rana/chronoverse/internal/service/analytics"
)

const (
	ExitOk = iota
	ExitError
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, cancel := svcpkg.Init()
	defer cancel()

	cfg, err := config.InitAnalyticsServiceConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	auth, err := auth.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

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

	repo := analyticsrepo.New(auth, pdb)

	validator := validator.New()

	svc := analyticssvc.New(validator, repo)

	app := analytics.New(ctx, &analytics.Config{
		Deadline:    cfg.Grpc.RequestTimeout,
		Environment: cfg.Environment.Env,
		TLSConfig: &analytics.TLSConfig{
			Enabled:  cfg.Grpc.TLS.Enabled,
			CAFile:   cfg.Grpc.TLS.CAFile,
			CertFile: cfg.Grpc.TLS.CertFile,
			KeyFile:  cfg.Grpc.TLS.KeyFile,
		},
	}, auth, svc)

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Grpc.Port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create listener: %v\n", err)
		return ExitError
	}

	go grpcserverpkg.GracefulStop(ctx, app, 20*time.Second)

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

	if err := app.Serve(listener); err != nil {
		if ctx.Err() != nil {
			return ExitOk
		}
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	return ExitOk
}
