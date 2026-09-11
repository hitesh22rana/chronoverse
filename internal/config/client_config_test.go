package config_test

import (
	"testing"
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/stretchr/testify/require"

	"github.com/hitesh22rana/chronoverse/internal/config"
	grpcclient "github.com/hitesh22rana/chronoverse/internal/pkg/grpc/client"
)

func TestClientConfigKeepsEnvironmentSettingsSeparate(t *testing.T) {
	for name, value := range map[string]string{
		"POSTGRES_HOST": "postgres.internal", "POSTGRES_PORT": "5433", "POSTGRES_MAX_CONNS": "23",
		"POSTGRES_MAX_CONN_LIFE": "7m", "POSTGRES_TLS_ENABLED": "true", "POSTGRES_TLS_CA_FILE": "pg-ca.pem",
		"REDIS_HOST": "redis.internal", "REDIS_PORT": "6380", "REDIS_DB": "4", "REDIS_READ_TIMEOUT": "2s",
		"REDIS_TLS_ENABLED": "false", "REDIS_TLS_CA_FILE": "redis-ca.pem", "REDIS_TLS_CERT_FILE": "redis-cert.pem",
		"CLICKHOUSE_HOSTS": "clickhouse.internal:9440,clickhouse2.internal:9440", "CLICKHOUSE_MAX_OPEN_CONNS": "17",
		"CLICKHOUSE_TLS_ENABLED": "true", "CLICKHOUSE_TLS_CA_FILE": "ch-ca.pem",
		"CLICKHOUSE_TLS_CERT_FILE": "ch-cert.pem", "CLICKHOUSE_TLS_KEY_FILE": "ch-key.pem",
		"CLIENT_TLS_CERT_FILE": "caller-cert.pem", "CLIENT_TLS_KEY_FILE": "caller-key.pem",
	} {
		t.Setenv(name, value)
	}
	var stores struct {
		config.Postgres
		config.Redis
		config.ClickHouse
		config.ClientTLS
	}
	require.NoError(t, envconfig.Process("", &stores))
	pg, redis, ch := stores.Postgres.ClientConfig(), stores.Redis.ClientConfig(), stores.ClickHouse.ClientConfig()
	require.Equal(t, "postgres.internal", pg.Host)
	require.Equal(t, 5433, pg.Port)
	require.Equal(t, int32(23), pg.MaxConns)
	require.Equal(t, 7*time.Minute, pg.MaxConnLife)
	require.True(t, pg.TLSConfig.Enabled)
	require.Equal(t, "pg-ca.pem", pg.TLSConfig.CAFile)
	require.Equal(t, "redis.internal", redis.Host)
	require.Equal(t, 6380, redis.Port)
	require.Equal(t, 4, redis.DB)
	require.Equal(t, 2*time.Second, redis.ReadTimeout)
	require.False(t, redis.TLSConfig.Enabled)
	require.Equal(t, "redis-ca.pem", redis.TLSConfig.CAFile)
	require.Equal(t, []string{"clickhouse.internal:9440", "clickhouse2.internal:9440"}, ch.Hosts)
	require.Equal(t, 17, ch.MaxOpenConns)
	require.True(t, ch.TLSConfig.Enabled)
	require.Equal(t, "ch-ca.pem", ch.TLSConfig.CAFile)
	require.Equal(t, "ch-cert.pem", ch.TLSConfig.CertFile)
	require.Equal(t, "ch-key.pem", ch.TLSConfig.KeyFile)

	for _, name := range []string{"USERS", "WORKFLOWS", "JOBS", "NOTIFICATIONS", "ANALYTICS"} {
		t.Setenv(name+"_SERVICE_HOST", name+".internal")
		t.Setenv(name+"_SERVICE_PORT", "50051")
		t.Setenv(name+"_SERVICE_TLS_ENABLED", "true")
		t.Setenv(name+"_SERVICE_TLS_CA_FILE", name+"-ca.pem")
	}
	var services struct {
		config.UsersService
		config.WorkflowsService
		config.JobsService
		config.NotificationsService
		config.AnalyticsService
	}
	require.NoError(t, envconfig.Process("", &services))
	for name, client := range map[string]*grpcclient.ServiceConfig{
		"USERS":         services.UsersService.ClientConfig(stores.ClientTLS),
		"WORKFLOWS":     services.WorkflowsService.ClientConfig(stores.ClientTLS),
		"JOBS":          services.JobsService.ClientConfig(stores.ClientTLS),
		"NOTIFICATIONS": services.NotificationsService.ClientConfig(stores.ClientTLS),
		"ANALYTICS":     services.AnalyticsService.ClientConfig(stores.ClientTLS),
	} {
		require.Equal(t, name+".internal", client.Host)
		require.Equal(t, name+"-ca.pem", client.TLS.CAFile)
		require.Equal(t, 50051, client.Port)
		require.True(t, client.TLS.Enabled)
		require.Equal(t, "caller-cert.pem", client.TLS.ClientCertFile)
		require.Equal(t, "caller-key.pem", client.TLS.ClientKeyFile)
	}
}
