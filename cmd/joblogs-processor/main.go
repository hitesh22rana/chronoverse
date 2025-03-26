package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"github.com/hitesh22rana/chronoverse/internal/app/joblogs"
	"github.com/hitesh22rana/chronoverse/internal/config"
	"github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	otelpkg "github.com/hitesh22rana/chronoverse/internal/pkg/otel"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
	joblogsrepo "github.com/hitesh22rana/chronoverse/internal/repository/joblogs"
	joblogssvc "github.com/hitesh22rana/chronoverse/internal/service/joblogs"
)

const (
	// ExitOk and ExitError are the exit codes.
	ExitOk = iota
	// ExitError is the exit code for errors.
	ExitError
)

var (
	// version is the service version.
	version string

	// name is the name of the service.
	name string

	// authPrivateKeyPath is the path to the private key.
	authPrivateKeyPath string

	// authPublicKeyPath is the path to the public key.
	authPublicKeyPath string
)

func main() {
	os.Exit(run())
}

func run() int {
	// Global context to cancel the execution
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize the service information
	initSvcInfo()

	// Handle OS signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
	}()

	// Load the joblogs service configuration
	cfg, err := config.InitJobLogsProcessorConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	// Initialize the OpenTelemetry Resource
	res, err := otelpkg.InitResource(ctx, svcpkg.Info().GetName(), svcpkg.Info().GetVersion())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	// Initialize the OpenTelemetry TracerProvider
	tp, err := otelpkg.InitTracerProvider(ctx, res)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer func() {
		if err = tp.Shutdown(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "failed to shutdown tracer provider: %v\n", err)
		}
	}()

	// Initialize the OpenTelemetry MeterProvider
	mp, err := otelpkg.InitMeterProvider(ctx, res)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer func() {
		if err = mp.Shutdown(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "failed to shutdown meter provider: %v\n", err)
		}
	}()

	// Initialize the OpenTelemetry LoggerProvider
	lp, err := otelpkg.InitLogProvider(ctx, res)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer func() {
		if err = lp.Shutdown(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "failed to shutdown logger provider: %v\n", err)
		}
	}()

	// Initialize and set the logger in the context
	ctx, logger := loggerpkg.Init(ctx, svcpkg.Info().GetName(), lp)
	defer func() {
		if err = logger.Sync(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to sync logger: %v\n", err)
		}
	}()

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
		kafka.WithConsumeTopics(cfg.Kafka.ConsumeTopics...),
		kafka.WithDisableAutoCommit(),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer kfk.Close()

	// Initialize the joblogs job components
	repo := joblogsrepo.New(&joblogsrepo.Config{
		BatchSizeLimit: cfg.JobLogsProcessorConfig.BatchSizeLimit,
		BatchTimeLimit: cfg.JobLogsProcessorConfig.BatchTimeLimit,
	}, cdb, kfk)
	svc := joblogssvc.New(repo)
	app := joblogs.New(ctx, svc)

	// Log the job information
	logger.Info(
		"starting job",
		zap.Any("ctx", ctx),
		zap.String("name", svcpkg.Info().Name),
		zap.String("version", svcpkg.Info().Version),
		zap.String("environment", cfg.Environment.Env),
	)

	// Run the joblogs job
	if err := app.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	return ExitOk
}

// initSvcInfo initializes the service information.
func initSvcInfo() {
	svcpkg.SetVersion(version)
	svcpkg.SetName(name)
	svcpkg.SetAuthPrivateKeyPath(authPrivateKeyPath)
	svcpkg.SetAuthPublicKeyPath(authPublicKeyPath)
}
