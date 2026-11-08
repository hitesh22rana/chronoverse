//nolint:testpackage // Tests private fixture ownership and I/O boundaries.
package testkit

import (
	"context"
	"errors"
	"net"
	"testing"
	"testing/synctest"
	"time"

	"github.com/moby/moby/api/types/network"
	"github.com/stretchr/testify/require"
	tcclickhouse "github.com/testcontainers/testcontainers-go/modules/clickhouse"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	clickhousepkg "github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	postgrespkg "github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	redispkg "github.com/hitesh22rana/chronoverse/internal/pkg/redis"
)

type mappedFixture struct {
	terminationContainer
	hostErr, portErr error
	port             network.Port
}

func (f mappedFixture) Host(context.Context) (string, error) { return "127.0.0.1", f.hostErr }
func (f mappedFixture) MappedPort(context.Context, string) (network.Port, error) {
	return f.port, f.portErr
}

func TestHostPortPreservesMappingFailures(t *testing.T) {
	cause := errors.New("mapping failed")
	for _, stage := range []string{"host", "mapping", "empty port", "success"} {
		t.Run(stage, func(t *testing.T) {
			fixture := mappedFixture{port: network.MustParsePort("15432/tcp")}
			switch stage {
			case "host":
				fixture.hostErr = cause
			case "mapping":
				fixture.portErr = cause
			case "empty port":
				fixture.port = network.Port{}
			}
			host, port, err := hostPort(t.Context(), fixture, "5432/tcp")
			if stage == "success" {
				require.NoError(t, err)
				require.Equal(t, "127.0.0.1", host)
				require.Equal(t, 15432, port)
				return
			}
			require.Error(t, err)
			require.Empty(t, host)
			require.Zero(t, port)
			if stage != "empty port" {
				require.ErrorIs(t, err, cause)
			}
		})
	}
}

func TestFreeHostPortRetainsListenFailureAndReleasesSocket(t *testing.T) {
	cause := errors.New("listen failed")
	port, err := freeHostPortWith(func(network, address string) (net.Listener, error) {
		require.Equal(t, "tcp", network)
		require.Equal(t, "127.0.0.1:0", address)
		return nil, cause
	})
	require.Empty(t, port)
	require.ErrorIs(t, err, cause)
	port, err = freeHostPort()
	require.NoError(t, err)
	require.NotEmpty(t, port)
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
	require.NoError(t, err)
	require.NoError(t, listener.Close())
}

func TestPostgresStartupFailureOwnershipAndRetryBudget(t *testing.T) {
	for _, stage := range []string{"container", "partial container", "host", "connect", "canceled", "cancel waiting", "migration", "retry success", "success"} {
		t.Run(stage, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) { checkPostgresStartup(t, stage) })
		})
	}
}

func checkPostgresStartup(t *testing.T, stage string) {
	t.Helper()
	cause := errors.New("postgres fixture failed")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s := &suite{}
	var calls, migrated, closed, terminated int
	client := &postgrespkg.Postgres{}
	fixture := mappedFixture{port: network.MustParsePort("15432/tcp"), terminationContainer: terminationContainer{terminate: func(context.Context) error { terminated++; return nil }}}
	if stage == "host" {
		fixture.hostErr = cause
	}
	startup := postgresStartup{
		startContainer: func(got context.Context) (*tcpostgres.PostgresContainer, error) {
			require.Equal(t, ctx, got)
			if stage == "container" {
				return nil, cause
			}
			ctr := &tcpostgres.PostgresContainer{Container: fixture}
			if stage == "partial container" {
				return ctr, cause
			}
			return ctr, nil
		},
		connect: func(got context.Context, cfg *postgrespkg.Config) (*postgrespkg.Postgres, error) {
			require.Equal(t, ctx, got)
			require.Equal(t, "127.0.0.1", cfg.Host)
			require.Equal(t, 15432, cfg.Port)
			require.Equal(t, postgresDB, cfg.Database)
			require.False(t, cfg.TLSConfig.Enabled)
			calls++
			switch stage {
			case "connect":
				return nil, cause
			case "canceled":
				cancel()
				return nil, cause
			case "cancel waiting":
				go func() { time.Sleep(500 * time.Millisecond); cancel() }()
				return nil, cause
			case "retry success":
				if calls == 1 {
					return nil, cause
				}
			}
			return client, nil
		},
		migrate: func(dsn string) error {
			migrated++
			require.Equal(t, "postgresql://primary:chronoverse@127.0.0.1:15432/chronoverse?sslmode=disable", dsn)
			if stage == "migration" {
				return cause
			}
			return nil
		},
		closeClient: func(got *postgrespkg.Postgres) { require.Same(t, client, got); closed++ },
	}
	start := time.Now()
	got, dsn, err := startPostgresWith(ctx, s, startup)
	success := stage == "success" || stage == "retry success"
	if success {
		require.NoError(t, err)
		require.Same(t, client, got)
		require.NotEmpty(t, dsn)
	} else {
		require.Error(t, err)
		require.Nil(t, got)
		require.Empty(t, dsn)
		if stage == "cancel waiting" {
			require.ErrorIs(t, err, context.Canceled)
		} else {
			require.ErrorIs(t, err, cause)
		}
	}
	require.Equal(t, map[string]int{"connect": 30, "canceled": 1, "cancel waiting": 1, "migration": 1, "retry success": 2, "success": 1}[stage], calls)
	require.Equal(t, map[bool]int{true: 1}[stage == "migration" || success], migrated)
	require.Equal(t, map[bool]int{true: 1}[stage == "migration"], closed)
	require.Equal(t, map[string]time.Duration{
		"connect":        29 * time.Second,
		"retry success":  time.Second,
		"cancel waiting": 500 * time.Millisecond,
	}[stage], time.Since(start))

	s.terminate(t.Context())
	s.terminate(t.Context())
	require.Equal(t, map[bool]int{true: 1}[stage != "container"], terminated)
}

func TestClickHouseStartupFailuresCloseOnlyOwnedResources(t *testing.T) {
	for _, stage := range []string{"container", "partial container", "address", "connect", "migration", "success"} {
		t.Run(stage, func(t *testing.T) { checkClickHouseStartup(t, stage) })
	}
}

func checkClickHouseStartup(t *testing.T, stage string) {
	t.Helper()
	cause := errors.New("clickhouse fixture failed")
	s := &suite{}
	closed, terminated := 0, 0
	client := &clickhousepkg.Client{}
	fixture := addressedFixture{terminationContainer: terminationContainer{terminate: func(context.Context) error { terminated++; return nil }}, address: "127.0.0.1:19000"}
	if stage == "address" {
		fixture.err = cause
	}
	startup := clickHouseStartup{
		startContainer: func(context.Context) (*tcclickhouse.ClickHouseContainer, error) {
			if stage == "container" {
				return nil, cause
			}
			ctr := &tcclickhouse.ClickHouseContainer{Container: fixture}
			if stage == "partial container" {
				return ctr, cause
			}
			return ctr, nil
		},
		connect: func(ctx context.Context, cfg *clickhousepkg.Config) (*clickhousepkg.Client, error) {
			require.Equal(t, t.Context(), ctx)
			require.Equal(t, []string{"127.0.0.1:19000"}, cfg.Hosts)
			require.Equal(t, clickHouseDatabase, cfg.Database)
			require.False(t, cfg.TLSConfig.Enabled)
			if stage == "connect" {
				return nil, cause
			}
			return client, nil
		},
		migrate: func(ctx context.Context, got *clickhousepkg.Client) error {
			require.Equal(t, t.Context(), ctx)
			require.Same(t, client, got)
			if stage == "migration" {
				return cause
			}
			return nil
		},
		closeClient: func(got *clickhousepkg.Client) error {
			require.Same(t, client, got)
			closed++
			return errors.New("close failed")
		},
	}
	got, addr, err := startClickHouseWith(t.Context(), s, startup)
	if stage == "success" {
		require.NoError(t, err)
		require.Same(t, client, got)
		require.Equal(t, fixture.address, addr)
	} else {
		require.ErrorIs(t, err, cause)
		require.Nil(t, got)
		require.Empty(t, addr)
	}
	require.Equal(t, map[bool]int{true: 1}[stage == "migration"], closed)
	s.terminate(t.Context())
	s.terminate(t.Context())
	require.Equal(t, map[bool]int{true: 1}[stage != "container"], terminated)
}

func TestRedisStartupPublishesOnlyAReadyEndpoint(t *testing.T) {
	for _, stage := range []string{"container", "partial container", "host", "connect", "success"} {
		t.Run(stage, func(t *testing.T) {
			cause := errors.New("redis fixture failed")
			terminated := 0
			s := &suite{redisAddr: "previous"}
			fixture := mappedFixture{port: network.MustParsePort("16379/tcp"), terminationContainer: terminationContainer{terminate: func(context.Context) error { terminated++; return nil }}}
			if stage == "host" {
				fixture.hostErr = cause
			}
			client := &redispkg.Store{}
			startup := redisStartup{
				startContainer: func(context.Context) (*tcredis.RedisContainer, error) {
					if stage == "container" {
						return nil, cause
					}
					ctr := &tcredis.RedisContainer{Container: fixture}
					if stage == "partial container" {
						return ctr, cause
					}
					return ctr, nil
				},
				connect: func(ctx context.Context, cfg *redispkg.Config) (*redispkg.Store, error) {
					require.Equal(t, t.Context(), ctx)
					require.Equal(t, "127.0.0.1", cfg.Host)
					require.Equal(t, 16379, cfg.Port)
					require.Equal(t, "100mb", cfg.MaxMemory)
					require.Equal(t, "volatile-ttl", cfg.EvictionPolicy)
					if stage == "connect" {
						return nil, cause
					}
					return client, nil
				},
			}
			got, err := startRedisWith(t.Context(), s, startup)
			if stage == "success" {
				require.NoError(t, err)
				require.Same(t, client, got)
				require.Equal(t, "127.0.0.1:16379", s.redisAddr)
			} else {
				require.ErrorIs(t, err, cause)
				require.Nil(t, got)
				require.Equal(t, "previous", s.redisAddr)
			}
			s.terminate(t.Context())
			s.terminate(t.Context())
			require.Equal(t, map[bool]int{true: 1}[stage != "container"], terminated)
		})
	}
}
