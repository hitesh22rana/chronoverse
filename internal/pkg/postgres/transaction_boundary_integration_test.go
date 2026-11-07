package postgres_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestMain(m *testing.M) { testkit.Run(m, testkit.WithPostgres()) }

func TestIntegrationTransactionOptionsAndNilDefaults(t *testing.T) {
	pg := testkit.Postgres(t)
	for _, options := range []*pgx.TxOptions{nil, {IsoLevel: pgx.Serializable, AccessMode: pgx.ReadOnly}} {
		tx, err := pg.BeginTxWithOptions(t.Context(), options)
		require.NoError(t, err)
		func() {
			defer func() {
				ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
				defer cancel()
				require.NoError(t, tx.Rollback(ctx))
			}()
			var isolation, readOnly string
			require.NoError(t, tx.QueryRow(t.Context(), "SHOW transaction_isolation").Scan(&isolation))
			require.NoError(t, tx.QueryRow(t.Context(), "SHOW transaction_read_only").Scan(&readOnly))
			if options == nil {
				require.Equal(t, "read committed", isolation)
				require.Equal(t, "off", readOnly)
			} else {
				require.Equal(t, "serializable", isolation)
				require.Equal(t, "on", readOnly)
			}
		}()
	}
}

type (
	brokenMeterProvider struct {
		metric.MeterProvider
		cause error
	}
	brokenMeter struct {
		metric.Meter
		cause error
	}
)

func (p brokenMeterProvider) Meter(name string, options ...metric.MeterOption) metric.Meter {
	return brokenMeter{Meter: noop.NewMeterProvider().Meter(name, options...), cause: p.cause}
}

func (m brokenMeter) Int64ObservableCounter(string, ...metric.Int64ObservableCounterOption) (metric.Int64ObservableCounter, error) {
	return nil, m.cause
}

func TestIntegrationConstructorReportsMetricsFailure(t *testing.T) {
	parsed, err := pgxpool.ParseConfig(testkit.PostgresDSN(t))
	require.NoError(t, err)
	previous := otel.GetMeterProvider()
	cause := errors.New("instrument registration rejected")
	otel.SetMeterProvider(brokenMeterProvider{MeterProvider: previous, cause: cause})
	t.Cleanup(func() { otel.SetMeterProvider(previous) })
	c := parsed.ConnConfig
	pg, err := postgres.New(t.Context(), &postgres.Config{Host: c.Host, Port: int(c.Port), User: c.User, Password: c.Password, Database: c.Database, TLSConfig: &postgres.TLSConfig{}})
	require.Nil(t, pg)
	require.Equal(t, codes.Internal, status.Code(err))
	require.Contains(t, err.Error(), "failed to record PostgreSQL stats")
	require.Contains(t, err.Error(), cause.Error())
}

func TestIntegrationDirtyMigrationRequiresRepairBeforeReplay(t *testing.T) {
	pg := testkit.Postgres(t)
	name := "migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err := pg.Exec(t.Context(), "CREATE DATABASE "+name)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		_, err := pg.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		require.NoError(t, err)
	})
	cfg, err := pgxpool.ParseConfig(testkit.PostgresDSN(t))
	require.NoError(t, err)
	cfg.ConnConfig.Database = name
	fresh, err := pgxpool.NewWithConfig(t.Context(), cfg)
	require.NoError(t, err)
	t.Cleanup(fresh.Close)
	_, err = fresh.Exec(t.Context(), "CREATE TABLE schema_migrations(version bigint PRIMARY KEY,dirty boolean NOT NULL); INSERT INTO schema_migrations VALUES(1,true)")
	require.NoError(t, err)
	dsn, parseErr := url.Parse(testkit.PostgresDSN(t))
	require.NoError(t, parseErr)
	dsn.Path = "/" + name
	err = postgres.Migrate(dsn.String())
	require.ErrorContains(t, err, "run postgres migrations")
	require.ErrorContains(t, err, "Dirty database version 1")
	_, err = fresh.Exec(t.Context(), "DELETE FROM schema_migrations")
	require.NoError(t, err)
	require.NoError(t, postgres.Migrate(dsn.String()))
	require.NoError(t, postgres.Migrate(dsn.String()))
	var exists bool
	require.NoError(t, fresh.QueryRow(t.Context(), "SELECT to_regclass('public.users') IS NOT NULL").Scan(&exists))
	require.True(t, exists)
}
