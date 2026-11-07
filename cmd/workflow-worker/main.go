package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"

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

	rdb, err := redis.New(ctx, cfg.Redis.ClientConfig())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer rdb.Close()

	cdb, err := clickhouse.New(ctx, cfg.ClickHouse.ClientConfig())
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
	defer msdb.Close()

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
	containerProvider, closeContainers := newContainerProvider(cfg, rdb)
	defer svcpkg.CloseAndLog(ctx, closeContainers, "failed to close docker endpoint clients")

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

	repo := workflowrepo.New(auth, rdb, cdb, msdb, kfk, kafkaLifecycle, &workflowrepo.Services{
		Workflows:       workflowspb.NewWorkflowsServiceClient(workflowsConn),
		Jobs:            jobpb.NewJobsServiceClient(jobsConn),
		Notifications:   notificationspb.NewNotificationsServiceClient(notificationsConn),
		CsvcForEndpoint: containerProvider,
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
		zap.Int64("gomemlimit", debug.SetMemoryLimit(-1)),
	)

	return svcpkg.WorkerExitCode(ctx, app.Run(ctx), os.Stderr)
}
