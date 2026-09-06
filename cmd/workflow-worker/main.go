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

	"github.com/hitesh22rana/chronoverse/internal/pkg/imagepull"
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
	"github.com/hitesh22rana/chronoverse/internal/pkg/meilisearch"
	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
	workflowrepo "github.com/hitesh22rana/chronoverse/internal/repository/workflow"
	workflowsvc "github.com/hitesh22rana/chronoverse/internal/service/workflow"
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

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
	}()

	cfg, err := config.InitWorkflowWorkerConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	auth, err := auth.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

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

	msdb, err := meilisearch.New(
		ctx,
		meilisearch.WithURI(cfg.MeiliSearch.URI),
		meilisearch.WithMasterKey(cfg.MeiliSearch.MasterKey),
		meilisearch.WithTLS(&cfg.MeiliSearch),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	msdb.Close()

	kafkaLifecycle := kafka.NewPartitionLifecycle()
	kfk, err := kafka.New(ctx,
		kafka.WithBrokers(cfg.Kafka.Brokers...),
		kafka.WithConsumerGroup(cfg.Kafka.ConsumerGroup),
		kafka.WithConsumeTopics(kafka.TopicWorkflows),
		kafka.WithDisableAutoCommit(),
		kafka.WithPartitionLifecycle(kafkaLifecycle),
		kafka.WithTLS(&cfg.Kafka),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer kfk.Close()

	// Workflow workers resolve image metadata through the runtime registry.
	// Runtime identity scopes image pull locks to the owning Docker daemon.
	imagePullLockConfig := imagepull.Config{
		TTL:           cfg.ImagePullLockTTL,
		WaitTimeout:   cfg.ImagePullLockWaitTimeout,
		RetryInterval: cfg.ImagePullLockRetryInterval,
	}
	dockerClients := container.NewEndpointCache(func(endpoint string) (*container.DockerWorkflow, error) {
		return container.NewDockerWorkflow(
			container.WithDockerHost(endpoint),
			container.WithDockerProxyTLS(container.DockerProxyTLSConfig{
				CAFile:     cfg.DockerProxy.TLS.CAFile,
				CertFile:   cfg.DockerProxy.TLS.CertFile,
				KeyFile:    cfg.DockerProxy.TLS.KeyFile,
				ServerName: cfg.DockerProxy.TLS.ServerName,
			}),
			container.WithDockerProxyToken(cfg.DockerProxy.Token),
		)
	})
	defer func() {
		if closeErr := dockerClients.Close(); closeErr != nil {
			loggerpkg.FromContext(ctx).Warn("failed to close docker endpoint clients", zap.Error(closeErr))
		}
	}()
	containerSvcForEndpoint := func(runtimeNodeID, endpoint string) (workflowrepo.ContainerSvc, error) {
		endpoint = container.NormalizeDockerProxyEndpoint(endpoint, cfg.DockerProxy.TLS.CAFile != "")
		csvc, csvcErr := dockerClients.Get(endpoint)
		if csvcErr != nil {
			return nil, csvcErr
		}
		cfg := imagePullLockConfig
		cfg.LockScope = runtimeNodeID
		return workflowrepo.NewImagePullLockedContainerSvc(csvc, rdb, cfg), nil
	}

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

	repo := workflowrepo.New(auth, rdb, cdb, msdb, kfk, kafkaLifecycle, &workflowrepo.Services{
		Workflows:       workflowspb.NewWorkflowsServiceClient(workflowsConn),
		Jobs:            jobpb.NewJobsServiceClient(jobsConn),
		Notifications:   notificationspb.NewNotificationsServiceClient(notificationsConn),
		CsvcForEndpoint: containerSvcForEndpoint,
		ImagePrefetch: workflowrepo.ImagePrefetchConfig{
			Enabled:   cfg.ImagePrefetchEnabled,
			MaxFanout: cfg.ImagePrefetchMaxFanout,
			Timeout:   cfg.ImagePullLockWaitTimeout,
		},
	})
	svc := workflowsvc.New(repo)
	app := workflow.New(ctx, svc)

	loggerpkg.FromContext(ctx).Info(
		"starting job",
		zap.Any("ctx", ctx),
		zap.String("name", svcpkg.Info().GetName()),
		zap.String("version", svcpkg.Info().GetVersion()),
		zap.String("environment", cfg.Environment.Env),
		zap.Int("gomaxprocs", runtime.GOMAXPROCS(0)),
		zap.Int64("gomemlimit", debug.SetMemoryLimit(0)),
	)

	if err := app.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	return ExitOk
}
