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

	userspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"

	"github.com/hitesh22rana/chronoverse/internal/app/notifications"
	"github.com/hitesh22rana/chronoverse/internal/config"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	grpcclient "github.com/hitesh22rana/chronoverse/internal/pkg/grpc/client"
	grpcserverpkg "github.com/hitesh22rana/chronoverse/internal/pkg/grpcserver"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
	notificationsrepo "github.com/hitesh22rana/chronoverse/internal/repository/notifications"
	notificationssvc "github.com/hitesh22rana/chronoverse/internal/service/notifications"
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

	cfg, err := config.InitNotificationsServiceConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	auth, err := auth.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	usersConn, err := grpcclient.NewClient(
		&grpcclient.ServiceConfig{
			Host: cfg.UsersService.Host,
			Port: cfg.UsersService.Port,
			TLS: &grpcclient.TLSConfig{
				Enabled:        cfg.UsersService.TLS.Enabled,
				CAFile:         cfg.UsersService.TLS.CAFile,
				ClientCertFile: cfg.ClientTLS.CertFile,
				ClientKeyFile:  cfg.ClientTLS.KeyFile,
			},
		},
		grpcclient.DefaultCircuitBreakerConfig(),
		grpcclient.DefaultRetryConfig(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return ExitError
	}
	defer usersConn.Close()

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

	repo := notificationsrepo.New(&notificationsrepo.Config{
		FetchLimit:            cfg.NotificationsServiceConfig.FetchLimit,
		EventCommandRetention: cfg.CommandIdempotency.EventRetention,
	}, auth, pdb, &notificationsrepo.Services{
		UsersService: userspb.NewUsersServiceClient(usersConn),
	})

	validator := validator.New()

	svc := notificationssvc.New(validator, repo)

	app := notifications.New(ctx, &notifications.Config{
		Deadline:    cfg.Grpc.RequestTimeout,
		Environment: cfg.Environment.Env,
		TLSConfig: &notifications.TLSConfig{
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
