package testkit

import (
	"context"
	"fmt"
	"time"

	tcclickhouse "github.com/testcontainers/testcontainers-go/modules/clickhouse"

	clickhousepkg "github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
)

const (
	clickHouseImage    = "clickhouse/clickhouse-server:25.8.7.3"
	clickHouseUser     = "chronoverse-client"
	clickHousePassword = "chronoverse"
	clickHouseDatabase = "chronoverse"
)

// startClickHouse starts a ClickHouse container and applies every embedded
// migration so repositories can be exercised against the real schema.
func startClickHouse(ctx context.Context, s *suite) (*clickhousepkg.Client, string, error) {
	ctr, err := tcclickhouse.Run(ctx, clickHouseImage,
		tcclickhouse.WithUsername(clickHouseUser),
		tcclickhouse.WithPassword(clickHousePassword),
		tcclickhouse.WithDatabase(clickHouseDatabase),
	)
	if err != nil {
		return nil, "", fmt.Errorf("start clickhouse container: %w", err)
	}
	s.containers = append(s.containers, ctr)

	addr, err := ctr.ConnectionHost(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("clickhouse address: %w", err)
	}

	ch, err := clickhousepkg.New(ctx, &clickhousepkg.Config{
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
	if err := clickhousepkg.Migrate(ctx, ch); err != nil {
		_ = ch.Close()
		return nil, "", fmt.Errorf("apply clickhouse migrations: %w", err)
	}

	return ch, addr, nil
}
