package databasemigration

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/meilisearch/meilisearch-go"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	clickhousepkg "github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	meilisearchpkg "github.com/hitesh22rana/chronoverse/internal/pkg/meilisearch"
	postgrespkg "github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
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

	// Execute migration with retry logic.
	if err = r.withRetry(ctx, "PostgreSQL", defaultRetryConfig(), func() error {
		return postgrespkg.Migrate(r.cfg.PostgresDSN)
	}); err != nil {
		err = status.Errorf(codes.Internal, "postgres migration failed after retries: %v", err)
		return err
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

	// Execute migration using native ClickHouse client with proper TLS support.
	if err = r.withRetry(ctx, "ClickHouse", defaultRetryConfig(), func() error {
		if r.cfg.ClickHouseClient == nil {
			return fmt.Errorf("clickhouse client is not configured")
		}
		return clickhousepkg.Migrate(ctx, r.cfg.ClickHouseClient)
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

	if err = meilisearchpkg.SetupIndexes(ctx, r.cfg.MeiliSearchClient); err != nil {
		err = status.Errorf(codes.Internal, "meilisearch setup failed: %v", err)
		return err
	}

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

		// Check if error is retryable.
		if !r.isDatabaseErrorRetryable(err) {
			logger.Error("database operation failed with non-retryable error",
				zap.String("database_type", dbType),
				zap.Error(err),
				zap.Int("attempt", attempt))
			return err
		}

		// Don't sleep on the last attempt.
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
				// Calculate next delay with exponential backoff.
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

	// Default to retryable for unknown errors.
	return true
}
