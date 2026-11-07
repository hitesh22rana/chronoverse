//nolint:testpackage // Covers private retry and migration-source boundaries without a server.
package postgres

import (
	"context"
	"testing"
	"testing/fstest"
	"testing/synctest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestClosedPoolHealthRetryBudgetAndCancellation(t *testing.T) {
	config, err := pgxpool.ParseConfig("postgres://localhost/unused?sslmode=disable")
	require.NoError(t, err)
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	require.NoError(t, err)
	pool.Close()
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		require.Error(t, healthCheck(t.Context(), pool))
		require.Equal(t, 32500*time.Millisecond, time.Since(start))
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, healthCheck(ctx, pool), context.Canceled)
}

func TestConstructorRejectsInvalidConfigurationAndCanceledStartup(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     Config
		code    codes.Code
		message string
	}{
		{"invalid URI", Config{Host: "%", Port: 5432, TLSConfig: &TLSConfig{}}, codes.InvalidArgument, "parse connection string"},
		{"invalid pool size", Config{Host: "localhost", Port: 5432, MaxConns: -1, TLSConfig: &TLSConfig{}}, codes.Internal, "create PostgreSQL pool"},
		{
			"TLS files",
			Config{Host: "localhost", Port: 5432, TLSConfig: &TLSConfig{Enabled: true, CAFile: "missing-ca", CertFile: "missing-cert", KeyFile: "missing-key"}},
			codes.InvalidArgument, "parse connection string",
		},
		{"canceled startup", Config{Host: "localhost", Port: 5432, TLSConfig: &TLSConfig{}}, codes.Internal, "check PostgreSQL health"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			pool, err := New(ctx, &tc.cfg)
			require.Nil(t, pool)
			require.Equal(t, tc.code, status.Code(err))
			require.Contains(t, err.Error(), tc.message)
		})
	}
}

func TestMigrationRejectsInvalidSourceBeforeOpeningDatabase(t *testing.T) {
	err := migrate("postgres://unused", fstest.MapFS{})
	require.ErrorContains(t, err, "create migration source")
	err = migrate("invalid://unused", migrationsFS)
	require.ErrorContains(t, err, "create migrate instance")
}
