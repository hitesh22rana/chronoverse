package testkit

import (
	"context"
	"fmt"
	"time"

	tcclickhouse "github.com/testcontainers/testcontainers-go/modules/clickhouse"

	clickhousepkg "github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
)

const (
	clickHouseImage    = "clickhouse/clickhouse-server:26.9.23.15"
	clickHouseUser     = "chronoverse-client"
	clickHousePassword = "chronoverse"
	clickHouseDatabase = "chronoverse"
)

// startClickHouse starts a ClickHouse container and applies every embedded
// migration so repositories can be exercised against the real schema.
func startClickHouse(ctx context.Context, s *suite) (*clickhousepkg.Client, string, error) {
	return startClickHouseWith(ctx, s, clickHouseStartup{
		startContainer: func(ctx context.Context) (*tcclickhouse.ClickHouseContainer, error) {
			return tcclickhouse.Run(ctx, clickHouseImage, tcclickhouse.WithUsername(clickHouseUser), tcclickhouse.WithPassword(clickHousePassword), tcclickhouse.WithDatabase(clickHouseDatabase))
		},
		connect:     clickhousepkg.New,
		migrate:     clickhousepkg.Migrate,
		closeClient: (*clickhousepkg.Client).Close,
	})
}

type clickHouseStartup struct {
	startContainer func(context.Context) (*tcclickhouse.ClickHouseContainer, error)
	connect        func(context.Context, *clickhousepkg.Config) (*clickhousepkg.Client, error)
	migrate        func(context.Context, *clickhousepkg.Client) error
	closeClient    func(*clickhousepkg.Client) error
}

func startClickHouseWith(ctx context.Context, s *suite, startup clickHouseStartup) (*clickhousepkg.Client, string, error) {
	ctr, err := startup.startContainer(ctx)
	if ctr != nil {
		s.containers = append(s.containers, ctr)
	}
	if err != nil {
		return nil, "", fmt.Errorf("start clickhouse container: %w", err)
	}

	addr, err := ctr.ConnectionHost(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("clickhouse address: %w", err)
	}

	ch, err := startup.connect(ctx, &clickhousepkg.Config{
		Hosts:           []string{addr},
		Database:        clickHouseDatabase,
		Username:        clickHouseUser,
		Password:        clickHousePassword,
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxLifetime: time.Hour,
		DialTimeout:     5 * time.Second,
		TLSConfig:       &clickhousepkg.TLSConfig{Enabled: false},
	})
	if err != nil {
		return nil, "", fmt.Errorf("connect clickhouse: %w", err)
	}

	// Reuse the production migration runner against the embedded migrations.
	if err := startup.migrate(ctx, ch); err != nil {
		//nolint:errcheck // Preserve the migration failure if best-effort cleanup also fails.
		_ = startup.closeClient(ch)
		return nil, "", fmt.Errorf("apply clickhouse migrations: %w", err)
	}

	return ch, addr, nil
}
