package databasemigration

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/meilisearch/meilisearch-go"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	clickhousepkg "github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	meilisearchpkg "github.com/hitesh22rana/chronoverse/internal/pkg/meilisearch"
	postgrespkg "github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	meiliSearchTaskTimeout     = 1 * time.Minute
	meiliSearchPollingDuration = 2 * time.Second

	// Database operation retry configuration.
	defaultMaxRetries    = 5
	defaultInitialDelay  = 1 * time.Second
	defaultMaxDelay      = 16 * time.Second
	defaultBackoffFactor = 2.0
)

var (
	// Network-related errors that are typically retryable.
	retryablePatterns = []string{
		"connection reset by peer",
		"connection refused",
		"timeout",
		"temporary failure",
		"network is unreachable",
		"no route to host",
		"connection timed out",
		"i/o timeout",
		"dial tcp",
		"broken pipe",
		"connection lost",
		"server closed",
		"connection aborted",
		"read: connection reset",
		"write: broken pipe",
	}

	// Non-retryable errors (authentication, certificate validation, syntax errors).
	nonRetryablePatterns = []string{
		"certificate",
		"authentication",
		"permission denied",
		"access denied",
		"invalid credentials",
		"tls",
		"ssl",
		"syntax error",
		"invalid query",
		"table already exists",
		"column already exists",
		"duplicate key",
		"constraint violation",
		"foreign key",
		"check constraint",
		"not null constraint",
		"unique constraint",
	}
)

// Config holds the database migration configuration.
type Config struct {
	PostgresDSN       string
	ClickHouseClient  *clickhousepkg.Client
	MeiliSearchClient meilisearch.ServiceManager
}

// RetryConfig holds configuration for database operation retries.
type RetryConfig struct {
	MaxRetries    int
	InitialDelay  time.Duration
	MaxDelay      time.Duration
	BackoffFactor float64
}

// Repository provides database migration repository.
type Repository struct {
	tp  trace.Tracer
	cfg *Config
}

// New creates a new database migration repository.
func New(cfg *Config) *Repository {
	return &Repository{
		tp:  otel.Tracer(svcpkg.Info().GetName()),
		cfg: cfg,
	}
}

// defaultRetryConfig returns the default retry configuration for database operations.
func defaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:    defaultMaxRetries,
		InitialDelay:  defaultInitialDelay,
		MaxDelay:      defaultMaxDelay,
		BackoffFactor: defaultBackoffFactor,
	}
}

// MigratePostgres migrates the PostgreSQL database.
func (r *Repository) MigratePostgres(ctx context.Context) (err error) {
	_, span := r.tp.Start(ctx, "Repository.MigratePostgres")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Execute migration with retry logic
	if err = r.withRetry(ctx, "PostgreSQL", defaultRetryConfig(), func() error {
		return r.runPostgresMigration(ctx)
	}); err != nil {
		err = status.Errorf(codes.Internal, "postgres migration failed after retries: %v", err)
		return err
	}

	return nil
}

// runPostgresMigration executes PostgreSQL migrations using the migrate library.
func (r *Repository) runPostgresMigration(ctx context.Context) error {
	// IOFS source instance for embedded migrations
	sourceInstance, err := iofs.New(postgrespkg.MigrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("failed to create IOFS source instance: %w", err)
	}

	// Migrate instance for postgres
	m, err := migrate.NewWithSourceInstance("iofs", sourceInstance, r.cfg.PostgresDSN)
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}

	// Ensure we close the migrate instance
	defer func() {
		if sourceErr, dbErr := m.Close(); sourceErr != nil || dbErr != nil {
			logger := loggerpkg.FromContext(ctx)
			logger.Error("failed to close PostgreSQL migrate instance",
				zap.Error(sourceErr),
				zap.Error(dbErr))
		}
	}()

	// Execute migration
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to run postgres migration: %w", err)
	}

	return nil
}

// MigrateClickHouse migrates the ClickHouse database with retry logic.
func (r *Repository) MigrateClickHouse(ctx context.Context) (err error) {
	_, span := r.tp.Start(ctx, "Repository.MigrateClickHouse")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Execute migration using native ClickHouse client with proper TLS support
	if err = r.withRetry(ctx, "ClickHouse", defaultRetryConfig(), func() error {
		return r.runNativeClickHouseMigration(ctx)
	}); err != nil {
		err = status.Errorf(codes.Internal, "clickhouse migration failed after retries: %v", err)
		return err
	}

	return nil
}

// SetupMeiliSearch setups the MeiliSearch database.
func (r *Repository) SetupMeiliSearch(ctx context.Context) (err error) {
	_, span := r.tp.Start(ctx, "Repository.SetupMeiliSearch")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	createIndexTaskIds := make([]int64, 0, len(meilisearchpkg.Indexes))
	for _, index := range meilisearchpkg.Indexes {
		createIndexTask, _err := r.cfg.MeiliSearchClient.CreateIndexWithContext(
			ctx,
			&meilisearch.IndexConfig{
				Uid:        index.Name,
				PrimaryKey: index.PrimaryKey,
			},
		)
		if _err != nil {
			err = status.Errorf(codes.Internal, "failed to create meilisearch index %v", _err)
			return err
		}

		createIndexTaskIds = append(createIndexTaskIds, createIndexTask.TaskUID)
	}

	if _err := r.waitForTaskCompletion(ctx, createIndexTaskIds); _err != nil {
		err = _err
		return err
	}

	updateSearchableTaskIds := make([]int64, 0, len(meilisearchpkg.Indexes))
	for _, index := range meilisearchpkg.Indexes {
		meiliSearchIndex := r.cfg.MeiliSearchClient.Index(index.Name)
		updateSearchableTask, _err := meiliSearchIndex.UpdateSearchableAttributesWithContext(
			ctx,
			&index.Searchable,
		)
		if _err != nil {
			err = status.Errorf(codes.Internal, "failed to update searchable index %v", _err)
			return err
		}

		updateSearchableTaskIds = append(updateSearchableTaskIds, updateSearchableTask.TaskUID)
	}

	if _err := r.waitForTaskCompletion(ctx, updateSearchableTaskIds); _err != nil {
		err = _err
		return err
	}

	updateFilterableTaskIds := make([]int64, 0, len(meilisearchpkg.Indexes))
	for _, index := range meilisearchpkg.Indexes {
		meiliSearchIndex := r.cfg.MeiliSearchClient.Index(index.Name)
		updateFilterableTask, _err := meiliSearchIndex.UpdateFilterableAttributesWithContext(
			ctx,
			&index.Filterable,
		)
		if _err != nil {
			err = status.Errorf(codes.Internal, "failed to update searchable index %v", _err)
			return err
		}

		updateFilterableTaskIds = append(updateFilterableTaskIds, updateFilterableTask.TaskUID)
	}

	if _err := r.waitForTaskCompletion(ctx, updateFilterableTaskIds); _err != nil {
		err = _err
		return err
	}

	updateSortableTaskIds := make([]int64, 0, len(meilisearchpkg.Indexes))
	for _, index := range meilisearchpkg.Indexes {
		meiliSearchIndex := r.cfg.MeiliSearchClient.Index(index.Name)
		updateSortableTask, _err := meiliSearchIndex.UpdateSortableAttributesWithContext(
			ctx,
			&index.Sortable,
		)
		if _err != nil {
			err = status.Errorf(codes.Internal, "failed to update searchable index %v", _err)
			return err
		}

		updateSortableTaskIds = append(updateSortableTaskIds, updateSortableTask.TaskUID)
	}

	err = r.waitForTaskCompletion(ctx, updateSortableTaskIds)
	return err
}

func (r *Repository) waitForTaskCompletion(ctx context.Context, taskIds []int64) error {
	if len(taskIds) == 0 {
		return nil
	}

	ticker := time.NewTicker(meiliSearchPollingDuration)
	defer ticker.Stop()

	timeout := time.After(meiliSearchTaskTimeout)
	for {
		select {
		case <-timeout:
			return status.Errorf(codes.DeadlineExceeded, "timeout waiting for Meilisearch tasks: %v", taskIds)
		case <-ticker.C:
			tasks, err := r.cfg.MeiliSearchClient.GetTasksWithContext(
				ctx,
				&meilisearch.TasksQuery{
					UIDS:  taskIds,
					Limit: int64(len(taskIds)),
				},
			)
			if err != nil {
				return status.Errorf(codes.Internal, "failed to get create meilisearch index info %v", err)
			}

			allDone := true
			//nolint:gocritic,exhaustive // It's how implemented in the library.
			for _, task := range tasks.Results {
				switch task.Status {
				case "succeeded":
				case "failed":
					if strings.Contains(task.Error.Code, "index_already_exists") {
						return nil
					}

					return status.Errorf(codes.Internal, "meilisearch task %d failed: %v", task.UID, task.Error)
				case "canceled":
					return status.Errorf(codes.Internal, "meilisearch task %d was canceled", task.UID)
				default:
					allDone = false
				}
			}

			if allDone {
				return nil
			}
		}
	}
}

// runNativeClickHouseMigration executes ClickHouse migrations using the native client.
func (r *Repository) runNativeClickHouseMigration(ctx context.Context) error {
	logger := loggerpkg.FromContext(ctx)

	// Use the pre-configured ClickHouse client
	client := r.cfg.ClickHouseClient
	if client == nil {
		return fmt.Errorf("ClickHouse client is not configured")
	}

	// Create schema_migrations table if it doesn't exist
	if err := r.createSchemaMigrationsTable(ctx, client); err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %w", err)
	}

	// Get applied migrations
	appliedMigrations, err := r.getAppliedMigrations(ctx, client)
	if err != nil {
		return fmt.Errorf("failed to get applied migrations: %w", err)
	}

	// Get pending migrations
	pendingMigrations, err := r.getPendingMigrations(appliedMigrations)
	if err != nil {
		return fmt.Errorf("failed to get pending migrations: %w", err)
	}

	if len(pendingMigrations) == 0 {
		logger.Info("No pending ClickHouse migrations")
		return nil
	}

	// Apply pending migrations
	for _, migration := range pendingMigrations {
		logger.Info("Applying ClickHouse migration", zap.String("file", migration.Name))

		if err := r.applyMigration(ctx, client, migration); err != nil {
			return fmt.Errorf("failed to apply migration %s: %w", migration.Name, err)
		}

		logger.Info("Successfully applied ClickHouse migration", zap.String("file", migration.Name))
	}

	logger.Info("ClickHouse migration completed successfully using native approach")
	return nil
}

// withRetry executes a database operation with exponential backoff retry logic.
func (r *Repository) withRetry(ctx context.Context, dbType string, config RetryConfig, operation func() error) error {
	logger := loggerpkg.FromContext(ctx)

	var lastErr error
	delay := config.InitialDelay

	for attempt := 1; attempt <= config.MaxRetries; attempt++ {
		logger.Info("attempting database operation",
			zap.String("database_type", dbType),
			zap.Int("attempt", attempt),
			zap.Int("max_attempts", config.MaxRetries))

		err := operation()
		if err == nil {
			if attempt > 1 {
				logger.Info("database operation succeeded after retries",
					zap.String("database_type", dbType),
					zap.Int("attempts", attempt))
			}
			return nil
		}

		lastErr = err

		// Check if error is retryable
		if !r.isDatabaseErrorRetryable(err) {
			logger.Error("database operation failed with non-retryable error",
				zap.String("database_type", dbType),
				zap.Error(err),
				zap.Int("attempt", attempt))
			return err
		}

		// Don't sleep on the last attempt
		if attempt < config.MaxRetries {
			logger.Warn("database operation failed, retrying",
				zap.String("database_type", dbType),
				zap.Error(err),
				zap.Int("attempt", attempt),
				zap.Duration("delay", delay))

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
				// Calculate next delay with exponential backoff
				delay = time.Duration(float64(delay) * config.BackoffFactor)
				delay = min(delay, config.MaxDelay)
			}
		}
	}

	logger.Error("database operation failed after all retries",
		zap.String("database_type", dbType),
		zap.Error(lastErr),
		zap.Int("max_attempts", config.MaxRetries))

	return lastErr
}

// Migration represents a database migration.
type Migration struct {
	Version int
	Name    string
	Content string
}

// createSchemaMigrationsTable creates the schema_migrations table.
func (r *Repository) createSchemaMigrationsTable(ctx context.Context, client *clickhousepkg.Client) error {
	query := `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version UInt32 NOT NULL,
			dirty UInt8 NOT NULL DEFAULT 0,
			applied_at DateTime DEFAULT now()
		) ENGINE = MergeTree()
		ORDER BY version
	`
	return client.Exec(ctx, query)
}

// getAppliedMigrations returns a map of applied migration versions.
func (r *Repository) getAppliedMigrations(ctx context.Context, client *clickhousepkg.Client) (map[int]bool, error) {
	applied := make(map[int]bool)

	rows, err := client.Query(ctx, "SELECT version FROM schema_migrations WHERE dirty = 0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var version uint32
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[int(version)] = true
	}

	return applied, rows.Err()
}

// getPendingMigrations returns migrations that haven't been applied yet.
func (r *Repository) getPendingMigrations(applied map[int]bool) ([]Migration, error) {
	var migrations []Migration

	// Read migration files from embedded filesystem
	err := fs.WalkDir(clickhousepkg.MigrationsFS, "migrations", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || !strings.HasSuffix(path, ".up.sql") {
			return nil
		}

		// Extract version from filename (e.g., "000001_table_job_logs_create.up.sql")
		filename := filepath.Base(path)
		parts := strings.Split(filename, "_")
		if len(parts) < 2 {
			return fmt.Errorf("invalid migration filename format: %s", filename)
		}

		version, err := strconv.Atoi(parts[0])
		if err != nil {
			return fmt.Errorf("invalid version in filename %s: %w", filename, err)
		}

		// Skip if already applied
		if applied[version] {
			return nil
		}

		// Read migration content
		content, err := fs.ReadFile(clickhousepkg.MigrationsFS, path)
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", path, err)
		}

		migrations = append(migrations, Migration{
			Version: version,
			Name:    filename,
			Content: string(content),
		})

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Sort migrations by version
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	return migrations, nil
}

// applyMigration applies a single migration.
func (r *Repository) applyMigration(ctx context.Context, client *clickhousepkg.Client, migration Migration) error {
	// Mark migration as dirty (in progress)
	if err := client.Exec(ctx, "INSERT INTO schema_migrations (version, dirty) VALUES (?, 1)", migration.Version); err != nil {
		return fmt.Errorf("failed to mark migration as dirty: %w", err)
	}

	// Execute migration SQL
	if err := client.Exec(ctx, migration.Content); err != nil {
		return fmt.Errorf("failed to execute migration SQL: %w", err)
	}

	// Mark migration as clean (completed)
	if err := client.Exec(ctx, "ALTER TABLE schema_migrations UPDATE dirty = 0 WHERE version = ?", migration.Version); err != nil {
		return fmt.Errorf("failed to mark migration as clean: %w", err)
	}

	return nil
}

// isDatabaseErrorRetryable determines if a database error is retryable.
func (r *Repository) isDatabaseErrorRetryable(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())

	for _, pattern := range retryablePatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	for _, pattern := range nonRetryablePatterns {
		if strings.Contains(errStr, pattern) {
			return false
		}
	}

	// Default to retryable for unknown errors
	return true
}
