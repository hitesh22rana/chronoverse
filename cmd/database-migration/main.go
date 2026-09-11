package main

import (
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"

	_ "github.com/KimMachineGun/automemlimit"
	_ "go.uber.org/automaxprocs"
	"go.uber.org/zap"

	"github.com/hitesh22rana/chronoverse/internal/app/databasemigration"
	"github.com/hitesh22rana/chronoverse/internal/config"
	"github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/meilisearch"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
	databasemigrationrepo "github.com/hitesh22rana/chronoverse/internal/repository/databasemigration"
	databasemigrationsvc "github.com/hitesh22rana/chronoverse/internal/service/databasemigration"
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

	cfg, err := config.InitDatabaseMigrationConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	// DSNs for database connections
	pgDSN := fmt.Sprintf(
		"postgresql://%s:%s@%s:%d/%s",
		url.QueryEscape(cfg.Postgres.User),
		url.QueryEscape(cfg.Postgres.Password),
		cfg.Postgres.Host,
		cfg.Postgres.Port,
		cfg.Postgres.Database,
	)
	if cfg.Postgres.TLS.Enabled {
		// Enable mutual TLS with full verification
		pgDSN += fmt.Sprintf("?sslmode=%s", "verify-full")

		// Append certificate paths if they are configured
		if cfg.Postgres.TLS.CAFile != "" {
			pgDSN += fmt.Sprintf("&sslrootcert=%s", url.QueryEscape(cfg.Postgres.TLS.CAFile))
		}
		if cfg.Postgres.TLS.CertFile != "" {
			pgDSN += fmt.Sprintf("&sslcert=%s", url.QueryEscape(cfg.Postgres.TLS.CertFile))
		}
		if cfg.Postgres.TLS.KeyFile != "" {
			pgDSN += fmt.Sprintf("&sslkey=%s", url.QueryEscape(cfg.Postgres.TLS.KeyFile))
		}
	} else {
		pgDSN += fmt.Sprintf("?sslmode=%s", "disable")
	}

	clickhouseClient, err := clickhouse.New(ctx, cfg.ClickHouse.ClientConfig())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer clickhouseClient.Close()

	meilisearchClient, err := meilisearch.New(
		ctx,
		meilisearch.WithURI(cfg.MeiliSearch.URI),
		meilisearch.WithMasterKey(cfg.MeiliSearch.MasterKey),
		meilisearch.WithTLS(&cfg.MeiliSearch),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	repo := databasemigrationrepo.New(&databasemigrationrepo.Config{
		PostgresDSN:       pgDSN,
		ClickHouseClient:  clickhouseClient,
		MeiliSearchClient: meilisearchClient,
	})
	svc := databasemigrationsvc.New(repo)
	app := databasemigration.New(ctx, svc)

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
