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
		cfg.UsersService.ClientConfig(cfg.ClientTLS),
		grpcclient.DefaultCircuitBreakerConfig(),
		grpcclient.DefaultRetryConfig(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return ExitError
	}
	defer usersConn.Close()

	pdb, err := postgres.New(ctx, cfg.Postgres.ClientConfig())
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
