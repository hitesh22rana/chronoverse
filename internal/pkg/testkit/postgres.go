package testkit

import (
	"context"
	"fmt"
	"time"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	postgrespkg "github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

const (
	postgresImage = "postgres:18.6-alpine3.24"
	postgresUser  = "primary"
	postgresPass  = "chronoverse"
	postgresDB    = "chronoverse"
)

// startPostgres starts a PostgreSQL container and applies every embedded
// migration so repositories can be exercised against the real schema.
func startPostgres(ctx context.Context, s *suite) (*postgrespkg.Postgres, string, error) {
	return startPostgresWith(ctx, s, postgresStartup{
		startContainer: func(ctx context.Context) (*tcpostgres.PostgresContainer, error) {
			return tcpostgres.Run(ctx, postgresImage, tcpostgres.WithDatabase(postgresDB), tcpostgres.WithUsername(postgresUser), tcpostgres.WithPassword(postgresPass))
		},
		connect:     postgrespkg.New,
		migrate:     postgrespkg.Migrate,
		closeClient: (*postgrespkg.Postgres).Close,
	})
}

type postgresStartup struct {
	startContainer func(context.Context) (*tcpostgres.PostgresContainer, error)
	connect        func(context.Context, *postgrespkg.Config) (*postgrespkg.Postgres, error)
	migrate        func(string) error
	closeClient    func(*postgrespkg.Postgres)
}

func startPostgresWith(ctx context.Context, s *suite, startup postgresStartup) (*postgrespkg.Postgres, string, error) {
	ctr, err := startup.startContainer(ctx)
	if ctr != nil {
		s.containers = append(s.containers, ctr)
	}
	if err != nil {
		return nil, "", fmt.Errorf("start postgres container: %w", err)
	}

	host, portNum, err := hostPort(ctx, ctr, "5432/tcp")
	if err != nil {
		return nil, "", fmt.Errorf("postgres: %w", err)
	}

	dsn := fmt.Sprintf("postgresql://%s:%s@%s:%d/%s?sslmode=disable",
		postgresUser, postgresPass, host, portNum, postgresDB)

	// The postgres image restarts once during first-boot initialization, which can
	// reset in-flight connections after the container is reported ready. Retry the
	// pool health check until the server accepts connections.
	var pg *postgrespkg.Postgres
	for attempt := 1; ; attempt++ {
		pg, err = startup.connect(ctx, &postgrespkg.Config{
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
		// Surface the in-flight connection failure for diagnostics; cancellation
		// that interrupts the retry wait is reported from the select below.
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
	if err := startup.migrate(dsn); err != nil {
		startup.closeClient(pg)
		return nil, "", fmt.Errorf("apply postgres migrations: %w", err)
	}

	return pg, dsn, nil
}
