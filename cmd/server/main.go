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

	// Load the server configuration
	cfg, err := config.InitServerConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	// Initialize the auth issuer
	auth, err := authpkg.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	// Initialize the crypto module
	crypto, err := crypto.New(cfg.Crypto.Secret)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	// Connect to the users service
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

	// Connect to the workflows service
	workflowsConn, err := grpcclient.NewClient(
		&grpcclient.ServiceConfig{
			Host: cfg.WorkflowsService.Host,
			Port: cfg.WorkflowsService.Port,
			TLS: &grpcclient.TLSConfig{
				Enabled:        cfg.WorkflowsService.TLS.Enabled,
				CAFile:         cfg.WorkflowsService.TLS.CAFile,
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
	defer workflowsConn.Close()

	// Connect to the jobs service
	jobsConn, err := grpcclient.NewClient(
		&grpcclient.ServiceConfig{
			Host: cfg.JobsService.Host,
			Port: cfg.JobsService.Port,
			TLS: &grpcclient.TLSConfig{
				Enabled:        cfg.JobsService.TLS.Enabled,
				CAFile:         cfg.JobsService.TLS.CAFile,
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
	defer jobsConn.Close()

	// Connect to the notifications service
	notificationsConn, err := grpcclient.NewClient(
		&grpcclient.ServiceConfig{
			Host: cfg.NotificationsService.Host,
			Port: cfg.NotificationsService.Port,
			TLS: &grpcclient.TLSConfig{
				Enabled:        cfg.NotificationsService.TLS.Enabled,
				CAFile:         cfg.NotificationsService.TLS.CAFile,
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
	defer notificationsConn.Close()

	// Connect to the analytics service
	analyticsConn, err := grpcclient.NewClient(
		&grpcclient.ServiceConfig{
			Host: cfg.AnalyticsService.Host,
			Port: cfg.AnalyticsService.Port,
			TLS: &grpcclient.TLSConfig{
				Enabled:        cfg.AnalyticsService.TLS.Enabled,
				CAFile:         cfg.AnalyticsService.TLS.CAFile,
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
	defer analyticsConn.Close()

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

	// Log the server information
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

	// Start the http server
	if err := srv.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	return ExitOk
}
