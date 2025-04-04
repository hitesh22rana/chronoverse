package main

import (
	"fmt"
	"os"

	"go.uber.org/zap"

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
		grpcclient.ServiceConfig{
			Host:     cfg.UsersService.Host,
			Port:     cfg.UsersService.Port,
			Secure:   cfg.UsersService.Secure,
			CertFile: cfg.UsersService.CertFile,
		}, grpcclient.DefaultRetryConfig())
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return ExitError
	}
	defer usersConn.Close()

	// Connect to the workflows service
	workflowsConn, err := grpcclient.NewClient(
		grpcclient.ServiceConfig{
			Host:     cfg.WorkflowsService.Host,
			Port:     cfg.WorkflowsService.Port,
			Secure:   cfg.WorkflowsService.Secure,
			CertFile: cfg.WorkflowsService.CertFile,
		}, grpcclient.DefaultRetryConfig())
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return ExitError
	}
	defer workflowsConn.Close()

	// Connect to the jobs service
	jobsConn, err := grpcclient.NewClient(
		grpcclient.ServiceConfig{
			Host:     cfg.JobsService.Host,
			Port:     cfg.JobsService.Port,
			Secure:   cfg.JobsService.Secure,
			CertFile: cfg.JobsService.CertFile,
		}, grpcclient.DefaultRetryConfig())
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return ExitError
	}
	defer jobsConn.Close()

	// Connect to the notifications service
	notificationsConn, err := grpcclient.NewClient(
		grpcclient.ServiceConfig{
			Host:     cfg.NotificationsService.Host,
			Port:     cfg.NotificationsService.Port,
			Secure:   cfg.NotificationsService.Secure,
			CertFile: cfg.NotificationsService.CertFile,
		}, grpcclient.DefaultRetryConfig())
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return ExitError
	}
	defer notificationsConn.Close()

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

	usersServiceClient := userpb.NewUsersServiceClient(usersConn)
	workflowsServiceClient := workflowspb.NewWorkflowsServiceClient(workflowsConn)
	jobsServiceClient := jobspb.NewJobsServiceClient(jobsConn)
	notificationsServiceClient := notificationspb.NewNotificationsServiceClient(notificationsConn)
	srv := server.New(ctx, &server.Config{
		Host:              cfg.Server.Host,
		Port:              cfg.Server.Port,
		RequestTimeout:    cfg.Server.RequestTimeout,
		ReadTimeout:       cfg.Server.ReadTimeout,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
		ValidationConfig: &server.ValidationConfig{
			SessionExpiry:    cfg.Server.SessionExpiry,
			CSRFExpiry:       cfg.Server.CSRFExpiry,
			RequestBodyLimit: cfg.Server.RequestBodyLimit,
			CSRFHMACSecret:   cfg.Server.CSRFHMACSecret,
		},
	}, auth, crypto, rdb, usersServiceClient, workflowsServiceClient, jobsServiceClient, notificationsServiceClient)

	// Log the server information
	loggerpkg.FromContext(ctx).Info(
		"starting server",
		zap.Any("ctx", ctx),
		zap.String("name", svcpkg.Info().GetName()),
		zap.String("version", svcpkg.Info().GetVersion()),
		zap.Int("port", cfg.Server.Port),
		zap.String("host", cfg.Server.Host),
		zap.String("env", cfg.Environment.Env),
	)

	// Start the http server
	if err := srv.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	return ExitOk
}
