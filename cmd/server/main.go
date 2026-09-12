package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"

	_ "github.com/KimMachineGun/automemlimit"
	_ "go.uber.org/automaxprocs"
	"go.uber.org/zap"

	analyticspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/analytics"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"
	userpb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	"github.com/hitesh22rana/chronoverse/internal/config"
	authpkg "github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	"github.com/hitesh22rana/chronoverse/internal/pkg/crypto"
	grpcclient "github.com/hitesh22rana/chronoverse/internal/pkg/grpc/client"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
	"github.com/hitesh22rana/chronoverse/internal/server"
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

	cfg, err := config.InitServerConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	auth, err := authpkg.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	crypto, err := crypto.New(cfg.Crypto.Secret)
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

	workflowsConn, err := grpcclient.NewClient(
		cfg.WorkflowsService.ClientConfig(cfg.ClientTLS),
		grpcclient.DefaultCircuitBreakerConfig(),
		grpcclient.DefaultRetryConfig(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return ExitError
	}
	defer workflowsConn.Close()

	jobsConn, err := grpcclient.NewClient(
		cfg.JobsService.ClientConfig(cfg.ClientTLS),
		grpcclient.DefaultCircuitBreakerConfig(),
		grpcclient.DefaultRetryConfig(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return ExitError
	}
	defer jobsConn.Close()

	notificationsConn, err := grpcclient.NewClient(
		cfg.NotificationsService.ClientConfig(cfg.ClientTLS),
		grpcclient.DefaultCircuitBreakerConfig(),
		grpcclient.DefaultRetryConfig(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return ExitError
	}
	defer notificationsConn.Close()

	analyticsConn, err := grpcclient.NewClient(
		cfg.AnalyticsService.ClientConfig(cfg.ClientTLS),
		grpcclient.DefaultCircuitBreakerConfig(),
		grpcclient.DefaultRetryConfig(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return ExitError
	}
	defer analyticsConn.Close()

	rdb, err := redis.New(ctx, cfg.Redis.ClientConfig())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer rdb.Close()

	srv := server.New(
		ctx,
		&server.Config{
			Host:              cfg.Server.Host,
			Port:              cfg.Server.Port,
			RequestTimeout:    cfg.Server.RequestTimeout,
			ReadTimeout:       cfg.Server.ReadTimeout,
			ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
			IdleTimeout:       cfg.Server.IdleTimeout,
			ValidationConfig: &server.ValidationConfig{
				SessionExpiry:    cfg.Server.SessionExpiry,
				CSRFExpiry:       cfg.Server.CSRFExpiry,
				RequestBodyLimit: cfg.Server.RequestBodyLimit,
				CSRFHMACSecret:   cfg.Server.CSRFHMACSecret,
			},
			HostURL:        cfg.Server.HostURL,
			AllowedOrigins: cfg.AllowedOrigins,
			SameSiteMode:   cfg.SameSiteMode,
			CookieDomain:   cfg.Server.CookieDomain,
		},
		auth,
		crypto,
		rdb,
		userpb.NewUsersServiceClient(usersConn),
		workflowspb.NewWorkflowsServiceClient(workflowsConn),
		jobspb.NewJobsServiceClient(jobsConn),
		notificationspb.NewNotificationsServiceClient(notificationsConn),
		analyticspb.NewAnalyticsServiceClient(analyticsConn),
	)

	loggerpkg.FromContext(ctx).Info(
		"starting server",
		zap.Any("ctx", ctx),
		zap.String("name", svcpkg.Info().GetName()),
		zap.String("version", svcpkg.Info().GetVersion()),
		zap.Int("port", cfg.Server.Port),
		zap.String("host", cfg.Server.Host),
		zap.String("env", cfg.Environment.Env),
		zap.Int("gomaxprocs", runtime.GOMAXPROCS(0)),
		zap.Int64("gomemlimit", debug.SetMemoryLimit(0)),
	)

	if err := srv.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	return ExitOk
}
