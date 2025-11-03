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

	// Load the database migration service configuration
	cfg, err := config.InitDatabaseMigrationConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	// DSN's for database connections
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
		// Use a non-TLS DSN if TLS is not enabled
		pgDSN += fmt.Sprintf("?sslmode=%s", "disable")
	}

	// Initialize the ClickHouse database client
	clickhouseClient, err := clickhouse.New(ctx, &clickhouse.Config{
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
	defer clickhouseClient.Close()

	// Initialize the MeiliSearch client
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

	// Initialize the database migration components
	repo := databasemigrationrepo.New(&databasemigrationrepo.Config{
		PostgresDSN:       pgDSN,
		ClickHouseClient:  clickhouseClient,
		MeiliSearchClient: meilisearchClient,
	})
	svc := databasemigrationsvc.New(repo)
	app := databasemigration.New(ctx, svc)

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

	// Run the scheduling job
	if err := app.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	return ExitOk
}
