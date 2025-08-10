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

	"github.com/hitesh22rana/chronoverse/internal/app/databasemigration"
	"github.com/hitesh22rana/chronoverse/internal/config"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
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
		"postgresql://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.Postgres.User,
		cfg.Postgres.Password,
		cfg.Postgres.Host,
		cfg.Postgres.Port,
		cfg.Postgres.Database,
		cfg.Postgres.SSLMode,
	)

	if len(cfg.ClickHouse.Hosts) == 0 {
		fmt.Fprintln(os.Stderr, "Error: ClickHouse hosts configuration is empty")
		return ExitError
	}
	chDSN := fmt.Sprintf(
		"clickhouse://%s?username=%s&password=%s&database=%s&x-multi-statement=true",
		cfg.ClickHouse.Hosts[0],
		cfg.ClickHouse.Username,
		cfg.ClickHouse.Password,
		cfg.ClickHouse.Database,
	)

	// Initialize the database migration components
	repo := databasemigrationrepo.New(&databasemigrationrepo.Config{
		PostgresDSN:   pgDSN,
		ClickHouseDSN: chDSN,
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
