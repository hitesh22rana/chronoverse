package main

import (
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strconv"
	"syscall"

	_ "github.com/KimMachineGun/automemlimit"
	_ "go.uber.org/automaxprocs"
	"go.uber.org/zap"

	"github.com/hitesh22rana/chronoverse/internal/app/outboxrelay"
	"github.com/hitesh22rana/chronoverse/internal/config"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
	outboxrelayrepo "github.com/hitesh22rana/chronoverse/internal/repository/outboxrelay"
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

	cfg, err := config.InitOutboxRelayConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

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

	kfk, err := kafka.New(ctx,
		kafka.WithBrokers(cfg.Kafka.Brokers...),
		kafka.WithTransactionalID(strconv.FormatInt(int64(os.Getpid()), 10)),
		kafka.WithTLS(&cfg.Kafka),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer kfk.Close()

	repo := outboxrelayrepo.New(&outboxrelayrepo.Config{
		BatchSize:    cfg.OutboxRelayConfig.BatchSize,
		MaxAttempts:  cfg.OutboxRelayConfig.MaxAttempts,
		RetryBackoff: cfg.OutboxRelayConfig.RetryBackoff,
		WorkerID:     cfg.OutboxRelayConfig.WorkerID,
	}, pdb, kfk)
	app := outboxrelay.New(ctx, &outboxrelay.Config{
		WorkflowEnabled:    cfg.OutboxRelayConfig.WorkflowEnabled,
		JobsEnabled:        cfg.OutboxRelayConfig.JobsEnabled,
		AnalyticsEnabled:   cfg.OutboxRelayConfig.AnalyticsEnabled,
		PollInterval:       cfg.OutboxRelayConfig.PollInterval,
		ContextTimeout:     cfg.OutboxRelayConfig.ContextTimeout,
		CleanupEnabled:     cfg.OutboxRelayConfig.CleanupEnabled,
		CleanupInterval:    cfg.OutboxRelayConfig.CleanupInterval,
		CleanupBatchSize:   cfg.OutboxRelayConfig.CleanupBatchSize,
		PublishedRetention: cfg.OutboxRelayConfig.PublishedRetention,
	}, repo)

	loggerpkg.FromContext(ctx).Info(
		"starting outbox relay",
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
