package main_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

// These commands previously only reached their dependency error paths. Real
// stores and brokers let the native process advance through startup, run its
// main loop, and shut down through the production defers.
func TestIntegrationNativeAnalyticsProcessorLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("real Kafka integration")
	}
	dependencies := append(postgresEnv(t), "KAFKA_BROKERS="+testkit.KafkaSeed(t))
	group := "native-analytics-processor-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	changes := append(append([]string{}, dependencies...),
		"KAFKA_CONSUMER_GROUP="+group,
		"ANALYTICS_PROCESSOR_CLEANUP_ENABLED=false",
	)
	client := testkit.Kafka(t)
	bin, cover := build(t, "analytics-processor")
	dir := keys(t, "analytics-processor")
	runNativeLifecycle(t, bin, dir, cover, changes, func(ctx context.Context, done <-chan error, output *outputBuffer) {
		waitReady(ctx, t, done, output, func() bool {
			return assignedNativeWorker(ctx, t, client, group, kafka.TopicAnalytics)
		})
	})
	reject(t, bin, dir, cover, "missing-client.pem", append(append([]string{}, dependencies...),
		"KAFKA_TLS_ENABLED=true",
		"KAFKA_TLS_CERT_FILE=missing-client.pem",
		"KAFKA_TLS_KEY_FILE=missing-client.key",
	)...)
	assertCoverage(t, cover, "analytics-processor")
}

func TestIntegrationNativeOutboxRelayLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("real Kafka integration")
	}
	dependencies := append(postgresEnv(t), "KAFKA_BROKERS="+testkit.KafkaSeed(t))
	changes := append(append([]string{}, dependencies...),
		"OUTBOX_RELAY_CLEANUP_ENABLED=false",
		"OUTBOX_RELAY_POLL_INTERVAL=50ms",
	)
	eventKey := "native-outbox-relay-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := testkit.Postgres(t).Exec(t.Context(),
		`INSERT INTO outbox_events (topic, kafka_key, event_key, payload) VALUES ($1, $2, $3, $4::jsonb)`,
		kafka.TopicAnalytics, "native-outbox-relay", eventKey, "{}",
	); err != nil {
		t.Fatalf("seed outbox event: %v", err)
	}
	bin, cover := build(t, "outbox-relay")
	dir := keys(t, "outbox-relay")
	runNativeLifecycle(t, bin, dir, cover, changes, func(ctx context.Context, done <-chan error, output *outputBuffer) {
		waitReady(ctx, t, done, output, func() bool {
			return strings.Contains(output.String(), "published outbox events")
		})
	})
	reject(t, bin, dir, cover, "missing-client.pem", append(append([]string{}, dependencies...),
		"KAFKA_TLS_ENABLED=true",
		"KAFKA_TLS_CERT_FILE=missing-client.pem",
		"KAFKA_TLS_KEY_FILE=missing-client.key",
	)...)
	assertCoverage(t, cover, "outbox-relay")
}

func TestIntegrationNativeDatabaseMigrationLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("real store integration")
	}
	dependencies := append(postgresEnv(t), searchStoreEnv(t)...)
	bin, cover := build(t, "database-migration")
	dir := keys(t, "database-migration")
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	cmd.Dir = dir
	cmd.Env = environment(cover, dependencies...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("database migration did not finish: %v\n%s", ctx.Err(), out)
	}
	if err != nil {
		t.Fatalf("database migration: %v\n%s", err, out)
	}
	reject(t, bin, dir, cover, "missing-client.pem", append(append([]string{}, dependencies...),
		"MEILISEARCH_TLS_ENABLED=true",
		"MEILISEARCH_TLS_CERT_FILE=missing-client.pem",
		"MEILISEARCH_TLS_KEY_FILE=missing-client.key",
	)...)
	reject(t, bin, dir, cover, "meilisearch setup failed", append(append([]string{}, dependencies...),
		"MEILISEARCH_URI=http://127.0.0.1:1",
	)...)
	assertCoverage(t, cover, "database-migration")
}

func TestIntegrationNativeRuntimeAgentLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("real PostgreSQL integration")
	}
	docker := fakeDockerPing(t)
	runtimeID := "native-runtime-agent-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	changes := append(postgresEnv(t),
		"RUNTIME_AGENT_ID="+runtimeID,
		"RUNTIME_AGENT_NODE_NAME="+runtimeID,
		"RUNTIME_AGENT_DOCKER_ENDPOINT="+docker,
		"RUNTIME_AGENT_DOCKER_HEALTH_ENDPOINT="+docker,
		"RUNTIME_AGENT_HEARTBEAT_INTERVAL=50ms",
	)
	bin, cover := build(t, "runtime-agent")
	dir := keys(t, "runtime-agent")
	pg := testkit.Postgres(t)
	runNativeLifecycle(t, bin, dir, cover, changes, func(ctx context.Context, done <-chan error, output *outputBuffer) {
		waitReady(ctx, t, done, output, func() bool {
			return strings.Contains(output.String(), "starting runtime agent")
		})
		var firstHeartbeat time.Time
		waitReady(ctx, t, done, output, func() bool {
			var heartbeat time.Time
			if err := pg.QueryRow(ctx,
				`SELECT last_heartbeat_at FROM runtime_nodes WHERE id = $1`, runtimeID,
			).Scan(&heartbeat); err != nil {
				return false
			}
			if firstHeartbeat.IsZero() {
				firstHeartbeat = heartbeat
				return false
			}
			return heartbeat.After(firstHeartbeat)
		})
	})
	reject(t, bin, dir, cover, "failed to ping runtime Docker endpoint", append(postgresEnv(t),
		"RUNTIME_AGENT_DOCKER_HEALTH_ENDPOINT=tcp://127.0.0.1:1",
	)...)
	reject(t, bin, dir, cover, "workload firewall not ready", append(postgresEnv(t),
		"RUNTIME_AGENT_DOCKER_ENDPOINT="+docker,
		"RUNTIME_AGENT_DOCKER_HEALTH_ENDPOINT="+docker,
		"RUNTIME_AGENT_FIREWALL_READY_FILE=missing-firewall-ready",
	)...)
	reject(t, bin, dir, cover, "runtime agent id, node name",
		"RUNTIME_AGENT_DOCKER_ENDPOINT=", "RUNTIME_AGENT_DOCKER_HEALTH_ENDPOINT=")
	assertCoverage(t, cover, "runtime-agent")
}

func TestIntegrationNativeExecutionWorkerLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("real Redis and Kafka integration")
	}
	dependencies := append(redisEnv(t), "KAFKA_BROKERS="+testkit.KafkaSeed(t))
	group := "native-execution-worker-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	changes := append(append([]string{}, dependencies...),
		"KAFKA_CONSUMER_GROUP="+group,
		"EXECUTION_WORKER_CONCURRENCY=1",
		"EXECUTION_WORKER_AWAITING_RECONCILIATION_LIMIT=1",
	)
	client := testkit.Kafka(t)
	bin, cover := build(t, "execution-worker")
	dir := keys(t, "execution-worker")
	runNativeLifecycle(t, bin, dir, cover, changes, func(ctx context.Context, done <-chan error, output *outputBuffer) {
		waitReady(ctx, t, done, output, func() bool {
			return assignedNativeWorker(ctx, t, client, group, kafka.TopicJobs)
		})
	})
	assertCoverage(t, cover, "execution-worker")
}

// fakeDockerPing answers the Docker client's /_ping handshake so the runtime
// agent can start without a real daemon.
func fakeDockerPing(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Api-Version", "1.51")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	return "tcp://" + strings.TrimPrefix(server.URL, "http://")
}
