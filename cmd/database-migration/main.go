package main

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"

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

	cfg, err := config.InitDatabaseMigrationConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	pgDSN := postgresMigrationDSN(cfg.Postgres)

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
		zap.Int64("gomemlimit", debug.SetMemoryLimit(-1)),
	)

	if err := app.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	return ExitOk
}

// postgresMigrationDSN preserves credentials and certificate paths as URL values.
func postgresMigrationDSN(cfg config.Postgres) string {
	dsn := url.URL{
		Scheme: "postgresql",
		User:   url.UserPassword(cfg.User, cfg.Password),
		Host:   net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Path:   "/" + cfg.Database,
	}
	query := url.Values{}
	if cfg.TLS.Enabled {
		query.Set("sslmode", "verify-full")
		for key, value := range map[string]string{"sslrootcert": cfg.TLS.CAFile, "sslcert": cfg.TLS.CertFile, "sslkey": cfg.TLS.KeyFile} {
			if value != "" {
				query.Set(key, value)
			}
		}
	} else {
		query.Set("sslmode", "disable")
	}
	dsn.RawQuery = query.Encode()
	return dsn.String()
}
