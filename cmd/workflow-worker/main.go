package main

import (
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"

	_ "github.com/KimMachineGun/automemlimit"
	_ "go.uber.org/automaxprocs"
	"go.uber.org/zap"

	jobpb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	"github.com/hitesh22rana/chronoverse/internal/app/workflow"
	"github.com/hitesh22rana/chronoverse/internal/config"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	"github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	grpcclient "github.com/hitesh22rana/chronoverse/internal/pkg/grpc/client"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
	workflowrepo "github.com/hitesh22rana/chronoverse/internal/repository/workflow"
	workflowsvc "github.com/hitesh22rana/chronoverse/internal/service/workflow"
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

	// Handle OS signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
	}()

	// Load the workflow service configuration
	cfg, err := config.InitWorkflowWorkerConfig()
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

	// Initialize the ClickHouse database
	cdb, err := clickhouse.New(ctx, &clickhouse.Config{
		Hosts:           cfg.ClickHouse.Hosts,
		Database:        cfg.ClickHouse.Database,
		Username:        cfg.ClickHouse.Username,
		Password:        cfg.ClickHouse.Password,
		MaxOpenConns:    cfg.ClickHouse.MaxOpenConns,
		MaxIdleConns:    cfg.ClickHouse.MaxIdleConns,
		ConnMaxLifetime: cfg.ClickHouse.ConnMaxLifetime,
		DialTimeout:     cfg.ClickHouse.DialTimeout,
		TLSConfig: &clickhouse.TLSConfig{
			Enabled:  cfg.ClickHouse.TLS.Enabled,
			CAFile:   cfg.ClickHouse.TLS.CAFile,
			CertFile: cfg.ClickHouse.TLS.CertFile,
			KeyFile:  cfg.ClickHouse.TLS.KeyFile,
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer cdb.Close()

	// Initialize the kafka client
	kfk, err := kafka.New(ctx,
		kafka.WithBrokers(cfg.Kafka.Brokers...),
		kafka.WithConsumerGroup(cfg.Kafka.ConsumerGroup),
		kafka.WithConsumeTopics(kafka.TopicWorkflows),
		kafka.WithDisableAutoCommit(),
		kafka.WithTLS(&cfg.Kafka),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer kfk.Close()

	// Initialize the container service
	csvc, err := container.NewDockerWorkflow()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

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

	// Initialize the workflow job components
	repo := workflowrepo.New(&workflowrepo.Config{
		ParallelismLimit: runtime.GOMAXPROCS(0),
	}, auth, rdb, cdb, kfk, &workflowrepo.Services{
		Workflows:     workflowspb.NewWorkflowsServiceClient(workflowsConn),
		Jobs:          jobpb.NewJobsServiceClient(jobsConn),
		Notifications: notificationspb.NewNotificationsServiceClient(notificationsConn),
		Csvc:          csvc,
	})
	svc := workflowsvc.New(repo)
	app := workflow.New(ctx, svc)

	// Log the job information
	loggerpkg.FromContext(ctx).Info(
		"starting job",
		zap.Any("ctx", ctx),
		zap.String("name", svcpkg.Info().GetName()),
		zap.String("version", svcpkg.Info().GetVersion()),
		zap.String("environment", cfg.Environment.Env),
		zap.Int("gomaxprocs", runtime.GOMAXPROCS(0)),
		zap.Int64("gomemlimit", debug.SetMemoryLimit(0)),
	)

	// Run the workflow job
	if err := app.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	return ExitOk
}
