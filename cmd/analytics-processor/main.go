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

	"github.com/hitesh22rana/chronoverse/internal/app/analyticsprocessor"
	"github.com/hitesh22rana/chronoverse/internal/config"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
	analyticsprocessorrepo "github.com/hitesh22rana/chronoverse/internal/repository/analyticsprocessor"
	analyticsprocessorsvc "github.com/hitesh22rana/chronoverse/internal/service/analyticsprocessor"
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

	cfg, err := config.InitAnalyticsProcessorConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	pdb, err := postgres.New(ctx, cfg.Postgres.ClientConfig())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer pdb.Close()

	kafkaLifecycle := kafka.NewPartitionLifecycle()
	kfk, err := kafka.New(ctx,
		kafka.WithBrokers(cfg.Kafka.Brokers...),
		kafka.WithConsumerGroup(cfg.Kafka.ConsumerGroup),
		kafka.WithConsumeTopics(kafka.TopicAnalytics),
		kafka.WithDisableAutoCommit(),
		kafka.WithPartitionLifecycle(kafkaLifecycle),
		kafka.WithTLS(&cfg.Kafka),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer kfk.Close()

	repo := analyticsprocessorrepo.New(pdb, kfk, kafkaLifecycle)
	svc := analyticsprocessorsvc.New(repo)
	app := analyticsprocessor.New(ctx, &analyticsprocessor.Config{
		CleanupEnabled:           cfg.AnalyticsProcessorConfig.CleanupEnabled,
		CleanupInterval:          cfg.AnalyticsProcessorConfig.CleanupInterval,
		CleanupBatchSize:         cfg.AnalyticsProcessorConfig.CleanupBatchSize,
		ProcessedEventsRetention: cfg.AnalyticsProcessorConfig.ProcessedEventsRetention,
	}, svc)

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
