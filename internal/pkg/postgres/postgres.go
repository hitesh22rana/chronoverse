package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// MaxHealthCheckRetries is the maximum number of retries for the health check.
	MaxHealthCheckRetries = 3
)

// Config holds PostgreSQL connection configuration.
type Config struct {
	Host        string
	Port        int
	User        string
	Password    string
	Database    string
	MaxConns    int32
	MinConns    int32
	MaxConnLife time.Duration
	MaxConnIdle time.Duration
	DialTimeout time.Duration
	SSLMode     string
}

// Store is the interface for the postgres store.
type Store interface {
	Close()
	BeginTx(ctx context.Context) (pgx.Tx, error)
	QueryRow(ctx context.Context, query string, args ...interface{}) pgx.Row
	Query(ctx context.Context, query string, args ...interface{}) (pgx.Rows, error)
	Exec(ctx context.Context, query string, args ...interface{}) (pgconn.CommandTag, error)
	ExecBatch(ctx context.Context, batch *pgx.Batch) error
	IsUniqueViolation(err error) bool
	IsNoRows(err error) bool
}

// Postgres represents a PostgreSQL connection pool.
type Postgres struct {
	pool *pgxpool.Pool
}

// healthCheck is used to check the health of the PostgreSQL connection.
func healthCheck(ctx context.Context, pool *pgxpool.Pool) error {
	var err error

	backoff := 100 * time.Millisecond
	for i := 1; i <= MaxHealthCheckRetries; i++ {
		if err = pool.Ping(ctx); err == nil {
			break
		}
		if i < MaxHealthCheckRetries {
			time.Sleep(backoff)
			backoff *= 2
		}
	}

	return err
}

// New creates a new PostgreSQL connection pool.
func New(ctx context.Context, cfg *Config) (*Postgres, error) {
	if cfg.MaxConns == 0 {
		cfg.MaxConns = 10
	}
	if cfg.MinConns == 0 {
		cfg.MinConns = 2
	}
	if cfg.MaxConnLife == 0 {
		cfg.MaxConnLife = time.Hour
	}
	if cfg.MaxConnIdle == 0 {
		cfg.MaxConnIdle = 30 * time.Minute
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 5 * time.Second
	}
	if cfg.SSLMode == "" {
		cfg.SSLMode = "disable"
	}

	connString := fmt.Sprintf(
		"postgresql://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.User,
		cfg.Password,
		cfg.Host,
		cfg.Port,
		cfg.Database,
		cfg.SSLMode,
	)

	poolConfig, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to parse connection string: %v", err)
	}

	// Configure pool settings
	poolConfig.MaxConns = cfg.MaxConns
	poolConfig.MinConns = cfg.MinConns
	poolConfig.MaxConnLifetime = cfg.MaxConnLife
	poolConfig.MaxConnIdleTime = cfg.MaxConnIdle
	poolConfig.ConnConfig.ConnectTimeout = cfg.DialTimeout
	poolConfig.ConnConfig.Tracer = otelpgx.NewTracer()

	// Create connection pool
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create PostgreSQL pool: %v", err)
	}

	// Check the health of the connection
	if err := healthCheck(ctx, pool); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to check PostgreSQL health: %v", err)
	}

	// Enable OpenTelemetry instrumentation for postgres
	if err := otelpgx.RecordStats(pool); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to record PostgreSQL stats: %v", err)
	}

	return &Postgres{
		pool: pool,
	}, nil
}

// Close closes the connection pool.
func (db *Postgres) Close() {
	db.pool.Close()
}

// BeginTx starts a new transaction.
func (db *Postgres) BeginTx(ctx context.Context) (pgx.Tx, error) {
	return db.pool.Begin(ctx)
}

// QueryRow executes a query that returns a single row.
func (db *Postgres) QueryRow(ctx context.Context, query string, args ...interface{}) pgx.Row {
	return db.pool.QueryRow(ctx, query, args...)
}

// Query executes a query that returns multiple rows.
func (db *Postgres) Query(ctx context.Context, query string, args ...interface{}) (pgx.Rows, error) {
	return db.pool.Query(ctx, query, args...)
}

// Exec executes a query that doesn't return rows.
func (db *Postgres) Exec(ctx context.Context, query string, args ...interface{}) (pgconn.CommandTag, error) {
	return db.pool.Exec(ctx, query, args...)
}

// ExecBatch executes multiple queries in a batch.
func (db *Postgres) ExecBatch(ctx context.Context, batch *pgx.Batch) error {
	br := db.pool.SendBatch(ctx, batch)
	defer br.Close()

	if _, err := br.Exec(); err != nil {
		return status.Errorf(codes.Internal, "failed to execute batch: %v", err)
	}

	return nil
}

// Stats returns connection pool statistics.
func (db *Postgres) Stats() *pgxpool.Stat {
	return db.pool.Stat()
}
