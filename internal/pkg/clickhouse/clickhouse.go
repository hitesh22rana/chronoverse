package clickhouse

import (
	"context"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	// MaxHealthCheckRetries is the maximum number of retries for the health check.
	MaxHealthCheckRetries = 3
)

// Config represents the configuration for the ClickHouse client.
type Config struct {
	Hosts           []string
	Database        string
	Username        string
	Password        string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	DialTimeout     time.Duration
	Debug           bool
}

// Client represents a ClickHouse client.
type Client struct {
	conn driver.Conn
}

// healthCheck checks the health of the ClickHouse connection.
func healthCheck(ctx context.Context, conn driver.Conn) error {
	var err error

	backoff := 100 * time.Millisecond
	for i := 1; i <= MaxHealthCheckRetries; i++ {
		if err = conn.Ping(ctx); err == nil {
			break
		}
		if i < MaxHealthCheckRetries {
			time.Sleep(backoff)
			backoff *= 2
		}
	}

	return err
}

// NewClient creates a new ClickHouse client.
func NewClient(ctx context.Context, cfg *Config) (*Client, error) {
	options := &clickhouse.Options{
		Addr: cfg.Hosts,
		Auth: clickhouse.Auth{
			Database: cfg.Database,
			Username: cfg.Username,
			Password: cfg.Password,
		},
		Settings: clickhouse.Settings{
			"max_execution_time": 60,
		},
		Compression: &clickhouse.Compression{
			Method: clickhouse.CompressionLZ4,
		},
		DialTimeout:     cfg.DialTimeout,
		MaxOpenConns:    cfg.MaxOpenConns,
		MaxIdleConns:    cfg.MaxIdleConns,
		ConnMaxLifetime: cfg.ConnMaxLifetime,
		Debug:           cfg.Debug,
		ClientInfo: clickhouse.ClientInfo{
			Products: []struct {
				Name    string
				Version string
			}{
				{
					Name:    svcpkg.Info().GetName(),
					Version: svcpkg.Info().GetVersion(),
				},
			},
		},
	}

	conn, err := clickhouse.Open(options)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to open clickhouse connection: %v", err)
	}

	c := &Client{
		conn: conn,
	}

	// Initial health check
	if err := healthCheck(ctx, conn); err != nil {
		return nil, status.Errorf(codes.Internal, "initial health check failed: %v", err)
	}

	return c, nil
}

// Close closes the ClickHouse connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// Exec executes a query without returning any rows.
func (c *Client) Exec(ctx context.Context, query string, args ...any) error {
	return c.conn.Exec(ctx, query, args...)
}

// Query executes a query that returns rows.
func (c *Client) Query(ctx context.Context, query string, args ...any) (driver.Rows, error) {
	return c.conn.Query(ctx, query, args...)
}

// BatchInsert performs a batch insert operation with the provided data.
// items should be a slice of structs or maps that match the table structure.
func (c *Client) BatchInsert(ctx context.Context, query string, prepareFn func(batch driver.Batch) error) error {
	batch, err := c.conn.PrepareBatch(ctx, query)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to prepare batch: %v", err)
	}

	if err := prepareFn(batch); err != nil {
		return status.Errorf(codes.Internal, "failed to prepare batch data: %v", err)
	}

	if err := batch.Send(); err != nil {
		return status.Errorf(codes.Internal, "failed to send batch: %v", err)
	}

	return nil
}
