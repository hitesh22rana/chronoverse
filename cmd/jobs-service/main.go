package main

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"runtime/debug"
	"time"

	_ "github.com/KimMachineGun/automemlimit"
	"github.com/go-playground/validator/v10"
	_ "go.uber.org/automaxprocs"
	"go.uber.org/zap"

	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	"github.com/hitesh22rana/chronoverse/internal/app/jobs"
	"github.com/hitesh22rana/chronoverse/internal/config"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	"github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	grpcclient "github.com/hitesh22rana/chronoverse/internal/pkg/grpc/client"
	grpcserverpkg "github.com/hitesh22rana/chronoverse/internal/pkg/grpcserver"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/meilisearch"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
	jobsrepo "github.com/hitesh22rana/chronoverse/internal/repository/jobs"
	jobssvc "github.com/hitesh22rana/chronoverse/internal/service/jobs"
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

	cfg, err := config.InitJobsServiceConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	auth, err := auth.New()
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

	rdb, err := redis.New(ctx, cfg.Redis.ClientConfig())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer rdb.Close()

	clickhouseCfg := cfg.ClickHouse.ClientConfig()
	// Preserve the existing jobs-service TLS source during config deduplication.
	clickhouseCfg.TLSConfig = &clickhouse.TLSConfig{
		Enabled:  cfg.Redis.TLS.Enabled,
		CAFile:   cfg.Redis.TLS.CAFile,
		CertFile: cfg.Redis.TLS.CertFile,
		KeyFile:  cfg.Redis.TLS.KeyFile,
	}
	cdb, err := clickhouse.New(ctx, clickhouseCfg)
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

	repo := jobsrepo.New(&jobsrepo.Config{
		FetchLimit:            cfg.JobsServiceConfig.FetchLimit,
		LogsFetchLimit:        cfg.JobsServiceConfig.LogsFetchLimit,
		RuntimeHeartbeatTTL:   cfg.JobsServiceConfig.RuntimeHeartbeatTTL,
		RuntimeLostAfter:      cfg.JobsServiceConfig.RuntimeLostAfter,
		EventCommandRetention: cfg.CommandIdempotency.EventRetention,
	}, auth, pdb, rdb, cdb, msdb, &jobsrepo.Services{
		Workflows: workflowspb.NewWorkflowsServiceClient(workflowsConn),
	})

	validator := validator.New()

	svc := jobssvc.New(validator, repo, rdb)

	app := jobs.New(ctx, &jobs.Config{
		Deadline:    cfg.Grpc.RequestTimeout,
		Environment: cfg.Environment.Env,
		TLSConfig: &jobs.TLSConfig{
			Enabled:  cfg.Grpc.TLS.Enabled,
			CAFile:   cfg.Grpc.TLS.CAFile,
			CertFile: cfg.Grpc.TLS.CertFile,
			KeyFile:  cfg.Grpc.TLS.KeyFile,
		},
	}, auth, svc)

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Grpc.Port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create listener: %v\n", err)
		return ExitError
	}

	go grpcserverpkg.GracefulStop(ctx, app, 20*time.Second)

	loggerpkg.FromContext(ctx).Info(
		"starting service",
		zap.Any("ctx", ctx),
		zap.String("name", svcpkg.Info().GetName()),
		zap.String("version", svcpkg.Info().GetVersion()),
		zap.String("address", listener.Addr().String()),
		zap.String("environment", cfg.Environment.Env),
		zap.Bool("tls_enabled", cfg.Grpc.TLS.Enabled),
		zap.Int("gomaxprocs", runtime.GOMAXPROCS(0)),
		zap.Int64("gomemlimit", debug.SetMemoryLimit(0)),
	)

	if err := app.Serve(listener); err != nil {
		if ctx.Err() != nil {
			return ExitOk
		}
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	return ExitOk
}
