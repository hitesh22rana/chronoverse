package config_test

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hitesh22rana/chronoverse/internal/config"
)

func clearConfigEnvironment(t *testing.T) {
	t.Helper()
	keys := map[string]bool{"NODE_NAME": true, "DOCKER_HOST": true}
	var collect func(reflect.Type)
	collect = func(typ reflect.Type) {
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if key := field.Tag.Get("envconfig"); key != "" {
				keys[key] = true
			}
			if field.Type.Kind() == reflect.Struct {
				collect(field.Type)
			}
		}
	}
	for _, cfg := range []any{
		config.AnalyticsProcessor{},
		config.AnalyticsConfig{},
		config.DatabaseMigration{},
		config.ExecutionWorker{},
		config.JobLogsProcessor{},
		config.JobsConfig{},
		config.NotificationsConfig{},
		config.OutboxRelay{},
		config.RuntimeAgent{},
		config.SchedulingWorker{},
		config.ServerConfig{},
		config.UsersConfig{},
		config.WorkflowWorker{},
		config.WorkflowsConfig{},
	} {
		collect(reflect.TypeOf(cfg))
	}
	for key := range keys {
		t.Setenv(key, "")
		require.NoError(t, os.Unsetenv(key))
	}
}

func initializerEnvironment(t *testing.T) {
	t.Helper()
	clearConfigEnvironment(t)

	for key, value := range map[string]string{
		"GRPC_PORT": "50051", "KAFKA_BROKERS": "broker-a:9092,broker-b:9092",
		"MEILISEARCH_MASTER_KEY": "test-key", "CRYPTO_SECRET": strings.Repeat("a", 32),
		"SERVER_CSRF_HMAC_SECRET": strings.Repeat("b", 32), "NODE_NAME": "test-node",
		"COMMAND_IDEMPOTENCY_EVENT_RETENTION": "336h",
	} {
		t.Setenv(key, value)
	}
	for _, service := range []string{"USERS", "WORKFLOWS", "JOBS", "NOTIFICATIONS", "ANALYTICS"} {
		t.Setenv(service+"_SERVICE_HOST", strings.ToLower(service)+".test")
		t.Setenv(service+"_SERVICE_PORT", "50051")
	}
}

func configInitializer[T any](init func() (*T, error)) func() (any, error) {
	return func() (any, error) { return init() }
}

func TestInitializersRejectMalformedEnvironment(t *testing.T) {
	cases := []struct {
		name, key string
		init      func() (any, error)
	}{
		{"analytics processor", "ANALYTICS_PROCESSOR_CLEANUP_INTERVAL", configInitializer(config.InitAnalyticsProcessorConfig)},
		{"analytics service", "POSTGRES_PORT", configInitializer(config.InitAnalyticsServiceConfig)},
		{"migration", "CLICKHOUSE_MAX_OPEN_CONNS", configInitializer(config.InitDatabaseMigrationConfig)},
		{"execution worker", "EXECUTION_WORKER_CONCURRENCY", configInitializer(config.InitExecutionJobConfig)},
		{"job logs", "JOBLOGS_PROCESSOR_BATCH_JOB_LOGS_TIME_INTERVAL", configInitializer(config.InitJobLogsProcessorConfig)},
		{"jobs", "JOBS_SERVICE_CONFIG_FETCH_LIMIT", configInitializer(config.InitJobsServiceConfig)},
		{"notifications", "NOTIFICATIONS_SERVICE_CONFIG_FETCH_LIMIT", configInitializer(config.InitNotificationsServiceConfig)},
		{"outbox", "OUTBOX_RELAY_POLL_INTERVAL", configInitializer(config.InitOutboxRelayConfig)},
		{"runtime agent", "RUNTIME_AGENT_DOCKER_ADVERTISE_PORT", configInitializer(config.InitRuntimeAgentConfig)},
		{"scheduler", "SCHEDULING_WORKER_BATCH_SIZE", configInitializer(config.InitSchedulingJobConfig)},
		{"server", "SERVER_PORT", configInitializer(config.InitServerConfig)},
		{"users", "POSTGRES_PORT", configInitializer(config.InitUsersServiceConfig)},
		{"workflow worker", "WORKFLOW_WORKER_IMAGE_PREFETCH_ENABLED", configInitializer(config.InitWorkflowWorkerConfig)},
		{"workflows", "WORKFLOWS_SERVICE_CONFIG_FETCH_LIMIT", configInitializer(config.InitWorkflowsServiceConfig)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			initializerEnvironment(t)
			t.Setenv(tc.key, "invalid")
			cfg, err := tc.init()
			require.ErrorContains(t, err, tc.key)
			require.Nil(t, cfg)
		})
	}
}

func TestExecutionWorkerConfiguration(t *testing.T) {
	initializerEnvironment(t)
	t.Run("host identity and defaults", func(t *testing.T) {
		t.Setenv("EXECUTION_WORKER_ID", "")
		hostname, err := os.Hostname()
		require.NoError(t, err)
		require.NotEmpty(t, hostname)
		cfg, err := config.InitExecutionJobConfig()
		require.NoError(t, err)
		require.Equal(t, hostname, cfg.WorkerID)
		require.Equal(t, 30*time.Second, cfg.LeaseDuration)
		require.Equal(t, 10*time.Second, cfg.LeaseRenewInterval)
		require.Equal(t, "512m", cfg.WorkloadMemory)
		require.Equal(t, float64(1), cfg.WorkloadCPUs)
		require.Equal(t, int64(256), cfg.WorkloadPidsLimit)
	})
	t.Run("overrides", func(t *testing.T) {
		for key, value := range map[string]string{
			"EXECUTION_WORKER_ID":                            "worker-explicit",
			"EXECUTION_WORKER_CONCURRENCY":                   "7",
			"EXECUTION_WORKER_LEASE_DURATION":                "45s",
			"EXECUTION_WORKER_WORKLOAD_CONTAINER_MEMORY":     "768m",
			"EXECUTION_WORKER_WORKLOAD_CONTAINER_CPUS":       "1.5",
			"EXECUTION_WORKER_WORKLOAD_CONTAINER_PIDS_LIMIT": "321",
			"EXECUTION_WORKER_HEARTBEAT_NAT64_PREFIXES":      "64:ff9b::/96,2001:db8::/96",
		} {
			t.Setenv(key, value)
		}
		cfg, err := config.InitExecutionJobConfig()
		require.NoError(t, err)
		require.Equal(t, "worker-explicit", cfg.WorkerID)
		require.Equal(t, 7, cfg.Concurrency)
		require.Equal(t, 45*time.Second, cfg.LeaseDuration)
		require.Equal(t, "768m", cfg.WorkloadMemory)
		require.Equal(t, 1.5, cfg.WorkloadCPUs)
		require.Equal(t, int64(321), cfg.WorkloadPidsLimit)
		require.Equal(t, []string{"64:ff9b::/96", "2001:db8::/96"}, cfg.HeartbeatNAT64Prefixes)
	})
}

func TestServiceInitializerOverrides(t *testing.T) {
	initializerEnvironment(t)
	t.Setenv("POSTGRES_PORT", "5434")
	t.Setenv("REDIS_DB", "9")
	t.Run("users", func(t *testing.T) {
		cfg, err := config.InitUsersServiceConfig()
		require.NoError(t, err)
		require.Equal(t, 5434, cfg.Postgres.Port)
		require.Equal(t, 9, cfg.Redis.DB)
	})
	t.Run("analytics", func(t *testing.T) {
		cfg, err := config.InitAnalyticsServiceConfig()
		require.NoError(t, err)
		require.Equal(t, 50051, cfg.Grpc.Port)
		require.Equal(t, 5434, cfg.Postgres.Port)
	})
	t.Run("workflows", func(t *testing.T) {
		t.Setenv("WORKFLOWS_SERVICE_CONFIG_FETCH_LIMIT", "27")
		cfg, err := config.InitWorkflowsServiceConfig()
		require.NoError(t, err)
		require.Equal(t, 27, cfg.FetchLimit)
		require.Equal(t, 5434, cfg.Postgres.Port)
	})
	t.Run("migration", func(t *testing.T) {
		cfg, err := config.InitDatabaseMigrationConfig()
		require.NoError(t, err)
		require.Equal(t, 5434, cfg.Postgres.Port)
		require.Equal(t, "test-key", cfg.MeiliSearch.MasterKey)
	})
	t.Run("analytics processor", func(t *testing.T) {
		t.Setenv("ANALYTICS_PROCESSOR_CLEANUP_ENABLED", "false")
		cfg, err := config.InitAnalyticsProcessorConfig()
		require.NoError(t, err)
		require.False(t, cfg.CleanupEnabled)
		require.Equal(t, []string{"broker-a:9092", "broker-b:9092"}, cfg.Kafka.Brokers)
	})
	t.Run("job logs", func(t *testing.T) {
		t.Setenv("JOBLOGS_PROCESSOR_BATCH_JOB_LOGS_SIZE_LIMIT", "42")
		cfg, err := config.InitJobLogsProcessorConfig()
		require.NoError(t, err)
		require.Equal(t, 42, cfg.BatchJobLogsSizeLimit)
		require.Equal(t, 2*time.Second, cfg.BatchJobLogsTimeInterval)
	})
	t.Run("scheduler", func(t *testing.T) {
		t.Setenv("SCHEDULING_WORKER_POLL_INTERVAL", "750ms")
		cfg, err := config.InitSchedulingJobConfig()
		require.NoError(t, err)
		require.Equal(t, 750*time.Millisecond, cfg.PollInterval)
		require.Equal(t, 1000, cfg.BatchSize)
	})
	t.Run("workflow worker", func(t *testing.T) {
		t.Setenv("WORKFLOW_WORKER_IMAGE_PREFETCH_ENABLED", "false")
		cfg, err := config.InitWorkflowWorkerConfig()
		require.NoError(t, err)
		require.False(t, cfg.ImagePrefetchEnabled)
		require.Equal(t, 8, cfg.ImagePrefetchMaxFanout)
		require.Equal(t, "workflows.test", cfg.WorkflowsService.Host)
	})
}

func TestInitializersEnforceValidation(t *testing.T) {
	initializerEnvironment(t)
	t.Run("server", func(t *testing.T) {
		cfg, err := config.InitServerConfig()
		require.NoError(t, err)
		require.Equal(t, strings.Repeat("a", 32), cfg.Secret)
		t.Setenv("CRYPTO_SECRET", "short")
		cfg, err = config.InitServerConfig()
		require.ErrorContains(t, err, "CRYPTO_SECRET must be 32 bytes")
		require.Nil(t, cfg)
	})
	for _, tc := range []struct {
		name string
		init func() (any, error)
	}{
		{"jobs", configInitializer(config.InitJobsServiceConfig)},
		{"notifications", configInitializer(config.InitNotificationsServiceConfig)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := tc.init()
			require.NoError(t, err)
			require.NotNil(t, cfg)
			t.Setenv("COMMAND_IDEMPOTENCY_EVENT_RETENTION", "1h")
			cfg, err = tc.init()
			require.ErrorContains(t, err, "COMMAND_IDEMPOTENCY_EVENT_RETENTION")
			require.Nil(t, cfg)
		})
	}
	t.Run("outbox", func(t *testing.T) {
		cfg, err := config.InitOutboxRelayConfig()
		require.NoError(t, err)
		require.True(t, cfg.CleanupEnabled)
		t.Setenv("OUTBOX_RELAY_IDEMPOTENCY_CLEANUP_MAX_BATCHES", "0")
		cfg, err = config.InitOutboxRelayConfig()
		require.ErrorContains(t, err, "OUTBOX_RELAY_IDEMPOTENCY_CLEANUP_MAX_BATCHES")
		require.Nil(t, cfg)
	})
}

func TestRuntimeAgentIdentityAndEndpoints(t *testing.T) {
	initializerEnvironment(t)
	t.Setenv("NODE_NAME", "")
	t.Setenv("RUNTIME_AGENT_NODE_NAME", "")
	t.Setenv("RUNTIME_AGENT_ID", "")
	t.Setenv("RUNTIME_AGENT_DOCKER_ADVERTISE_HOST", "")
	t.Setenv("RUNTIME_AGENT_DOCKER_ENDPOINT", "")
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:2376")
	hostname, err := os.Hostname()
	require.NoError(t, err)
	require.NotEmpty(t, hostname)
	cfg, err := config.InitRuntimeAgentConfig()
	require.NoError(t, err)
	require.Equal(t, hostname, cfg.NodeName)
	require.Equal(t, hostname, cfg.ID)
	require.Equal(t, "tcp://127.0.0.1:2376", cfg.DockerEndpoint)
	require.Equal(t, "tcp://127.0.0.1:2376", cfg.DockerHealthEndpoint)
	t.Run("explicit identity and health endpoint", func(t *testing.T) {
		t.Setenv("RUNTIME_AGENT_NODE_NAME", "named-node")
		t.Setenv("RUNTIME_AGENT_ID", "explicit-id")
		t.Setenv("RUNTIME_AGENT_DOCKER_HEALTH_ENDPOINT", "unix:///var/run/docker.sock")
		cfg, err := config.InitRuntimeAgentConfig()
		require.NoError(t, err)
		require.Equal(t, "named-node", cfg.NodeName)
		require.Equal(t, "explicit-id", cfg.ID)
		require.Equal(t, "unix:///var/run/docker.sock", cfg.DockerHealthEndpoint)
	})
}

func TestServerInitializerRejectsMalformedCookieURL(t *testing.T) {
	initializerEnvironment(t)
	t.Setenv("SERVER_HOST_URL", "://%")
	t.Setenv("SERVER_COOKIE_DOMAIN", "example.test")
	cfg, err := config.InitServerConfig()
	require.ErrorContains(t, err, "SERVER_HOST_URL must be a valid URL")
	require.Nil(t, cfg)
}
