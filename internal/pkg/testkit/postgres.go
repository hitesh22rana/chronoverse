package testkit

import (
	"context"
	"fmt"
	"strconv"
	"time"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	postgrespkg "github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

const (
	postgresImage = "postgres:18.0-alpine3.22"
	postgresUser  = "primary"
	postgresPass  = "chronoverse"
	postgresDB    = "chronoverse"
)

// startPostgres starts a PostgreSQL container and applies every embedded
// migration so repositories can be exercised against the real schema.
func startPostgres(ctx context.Context, s *suite) (*postgrespkg.Postgres, string, error) {
	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase(postgresDB),
		tcpostgres.WithUsername(postgresUser),
		tcpostgres.WithPassword(postgresPass),
	)
	if err != nil {
		return nil, "", fmt.Errorf("start postgres container: %w", err)
	}
	s.containers = append(s.containers, ctr)

	host, err := ctr.Host(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("postgres host: %w", err)
	}
	port, err := ctr.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return nil, "", fmt.Errorf("postgres port: %w", err)
	}
	portNum, err := strconv.Atoi(port.Port())
	if err != nil {
		return nil, "", fmt.Errorf("postgres port %q: %w", port.Port(), err)
	}

	dsn := fmt.Sprintf("postgresql://%s:%s@%s:%d/%s?sslmode=disable",
		postgresUser, postgresPass, host, portNum, postgresDB)

	// The postgres image restarts once during first-boot initialization, which can
	// reset in-flight connections after the container is reported ready. Retry the
	// pool health check until the server accepts connections.
	var pg *postgrespkg.Postgres
	for attempt := 1; ; attempt++ {
		pg, err = postgrespkg.New(ctx, &postgrespkg.Config{
			Host:        host,
			Port:        portNum,
			User:        postgresUser,
			Password:    postgresPass,
			Database:    postgresDB,
			MaxConns:    10,
			MinConns:    2,
			DialTimeout: 5 * time.Second,
			TLSConfig:   &postgrespkg.TLSConfig{Enabled: false},
		})
		if err == nil {
			break
		}
		if attempt >= 30 || ctx.Err() != nil {
			return nil, "", fmt.Errorf("connect postgres: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-time.After(time.Second):
		}
	}

	// Reuse the production migration runner against the embedded migrations.
	if err := postgrespkg.Migrate(dsn); err != nil {
		pg.Close()
		return nil, "", fmt.Errorf("apply postgres migrations: %w", err)
	}

	return pg, dsn, nil
}
