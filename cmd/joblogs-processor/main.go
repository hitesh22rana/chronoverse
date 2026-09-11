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

	"github.com/hitesh22rana/chronoverse/internal/app/joblogs"
	"github.com/hitesh22rana/chronoverse/internal/config"
	"github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/meilisearch"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
	joblogsrepo "github.com/hitesh22rana/chronoverse/internal/repository/joblogs"
	joblogssvc "github.com/hitesh22rana/chronoverse/internal/service/joblogs"
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

	cfg, err := config.InitJobLogsProcessorConfig()
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

	pg, err := postgres.New(ctx, cfg.Postgres.ClientConfig())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer pg.Close()

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
		kafka.WithConsumeTopics(kafka.TopicJobLogs),
		kafka.WithDisableAutoCommit(),
		kafka.WithPartitionLifecycle(kafkaLifecycle),
		kafka.WithTLS(&cfg.Kafka),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer kfk.Close()

	repo := joblogsrepo.New(&joblogsrepo.Config{
		BatchJobLogsSizeLimit:    cfg.JobLogsProcessorConfig.BatchJobLogsSizeLimit,
		BatchJobLogsTimeInterval: cfg.JobLogsProcessorConfig.BatchJobLogsTimeInterval,
	}, rdb, pg, cdb, msdb, kfk, kafkaLifecycle)
	svc := joblogssvc.New(repo)
	app := joblogs.New(ctx, svc)

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
