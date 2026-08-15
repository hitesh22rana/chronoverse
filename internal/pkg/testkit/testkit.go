// Package testkit bootstraps real infrastructure (PostgreSQL, ClickHouse, Redis,
// Meilisearch and Kafka) with Testcontainers for repository integration tests.
//
// Every repository package that wants integration tests declares a TestMain that
// calls Run with the services it needs, then uses the exported accessors
// (Postgres, ClickHouse, Redis, Meilisearch, Kafka) from its test files.
// Services are started lazily on first use, shared across all tests in the
// package, and terminated once the package test binary finishes.
//
// The package deliberately reuses the production client constructors and
// migration runners from the sibling packages under internal/pkg (postgres,
// clickhouse, meilisearch, redis, kafka), so integration tests exercise the
// exact same code paths as the running services.
//
// Integration tests self-skip when running with -short or when Docker is not
// available, so `go test ./...` keeps working in every environment and `make
// test/short` stays fast. `make test/integration` runs the full suite.
package testkit

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/meilisearch/meilisearch-go"
	"github.com/testcontainers/testcontainers-go"
	"github.com/twmb/franz-go/pkg/kgo"

	clickhousepkg "github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	postgrespkg "github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	redispkg "github.com/hitesh22rana/chronoverse/internal/pkg/redis"
)

// service identifies a piece of infrastructure the suite can provision.
type service string

const (
	servicePostgres    service = "postgres"
	serviceClickHouse  service = "clickhouse"
	serviceRedis       service = "redis"
	serviceMeilisearch service = "meilisearch"
	serviceKafka       service = "kafka"
)

// Option configures the services a test binary provisions.
type Option func(*suite)

// WithPostgres provisions a PostgreSQL container with all migrations applied.
func WithPostgres() Option { return func(s *suite) { s.enabled[servicePostgres] = true } }

// WithClickHouse provisions a ClickHouse container with all migrations applied.
func WithClickHouse() Option { return func(s *suite) { s.enabled[serviceClickHouse] = true } }

// WithRedis provisions a Redis container.
func WithRedis() Option { return func(s *suite) { s.enabled[serviceRedis] = true } }

// WithMeilisearch provisions a Meilisearch container with all indexes configured.
func WithMeilisearch() Option { return func(s *suite) { s.enabled[serviceMeilisearch] = true } }

// WithKafka provisions a Kafka broker (KRaft, plaintext) with the standard
// topics created.
func WithKafka() Option { return func(s *suite) { s.enabled[serviceKafka] = true } }

// suite is the shared test environment of the current test binary. Because each
// Go package compiles its own test binary, this singleton is naturally scoped to
// one repository package.
type suite struct {
	enabled map[service]bool

	mu         sync.Mutex
	started    map[service]bool
	startErrs  map[service]error
	containers []testcontainers.Container

	pg        *postgrespkg.Postgres
	pgDSN     string
	ch        *clickhousepkg.Client
	chAddr    string
	rdb       *redispkg.Store
	ms        meilisearch.ServiceManager
	kfk       *kgo.Client
	kafkaSeed string
}

var shared = &suite{
	enabled:   make(map[service]bool),
	started:   make(map[service]bool),
	startErrs: make(map[service]error),
}

// Run is meant to be called from a repository package's TestMain. It runs the
// package tests and terminates every started container afterwards.
func Run(m *testing.M, opts ...Option) {
	for _, opt := range opts {
		opt(shared)
	}

	code := m.Run()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	shared.terminate(ctx)
	cancel()

	os.Exit(code)
}

// Postgres returns the shared PostgreSQL pool, starting the container and
// applying all migrations on first use.
func Postgres(t *testing.T) *postgrespkg.Postgres {
	t.Helper()
	shared.start(t, servicePostgres)
	return shared.pg
}

// PostgresDSN returns the DSN of the shared PostgreSQL instance, starting it on
// first use. Useful for tests that open their own connections (e.g. migrations).
func PostgresDSN(t *testing.T) string {
	t.Helper()
	shared.start(t, servicePostgres)
	return shared.pgDSN
}

// ClickHouse returns the shared ClickHouse client, starting the container and
// applying all migrations on first use.
func ClickHouse(t *testing.T) *clickhousepkg.Client {
	t.Helper()
	shared.start(t, serviceClickHouse)
	return shared.ch
}

// ClickHouseAddr returns the host:port address of the shared ClickHouse
// container, starting it on first use.
func ClickHouseAddr(t *testing.T) string {
	t.Helper()
	shared.start(t, serviceClickHouse)
	return shared.chAddr
}

// Redis returns the shared Redis store, starting the container on first use.
func Redis(t *testing.T) *redispkg.Store {
	t.Helper()
	shared.start(t, serviceRedis)
	return shared.rdb
}

// Meilisearch returns the shared Meilisearch client, starting the container and
// configuring all indexes on first use.
func Meilisearch(t *testing.T) meilisearch.ServiceManager {
	t.Helper()
	shared.start(t, serviceMeilisearch)
	return shared.ms
}

// KafkaSeed returns the broker address (host:port) of the shared Kafka broker.
func KafkaSeed(t *testing.T) string {
	t.Helper()
	shared.start(t, serviceKafka)
	return shared.kafkaSeed
}

// Kafka returns a franz-go client connected to the shared Kafka broker. The
// client has no consumer group and is meant for producing records and admin
// operations. Consumers with their own group are created with KafkaConsumer.
func Kafka(t *testing.T) *kgo.Client {
	t.Helper()
	shared.start(t, serviceKafka)
	return shared.kfk
}

// KafkaConsumer returns a fresh consumer client with a dedicated consumer group
// that starts reading from the earliest offset of the given topics.
func KafkaConsumer(t *testing.T, group string, topics ...string) *kgo.Client {
	t.Helper()
	shared.start(t, serviceKafka)

	client, err := kgo.NewClient(
		kgo.SeedBrokers(shared.kafkaSeed),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(topics...),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.BlockRebalanceOnPoll(),
		kgo.DisableAutoCommit(),
	)
	if err != nil {
		t.Fatalf("failed to create kafka consumer: %v", err)
	}
	t.Cleanup(client.Close)
	return client
}

// start provisions the given service exactly once per test binary. Tests are
// skipped (never failed) when running in -short mode or when Docker is
// unavailable, so plain `go test` works on machines without a daemon.
func (s *suite) start(t *testing.T, svc service) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started[svc] {
		return
	}
	if s.startErrs[svc] != nil {
		t.Skipf("integration test skipped: %s testcontainer unavailable: %v", svc, s.startErrs[svc])
	}
	if !s.enabled[svc] {
		t.Fatalf("testkit: %s is not enabled for this package; add the matching With* option to TestMain", svc)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var err error
	switch svc {
	case servicePostgres:
		s.pg, s.pgDSN, err = startPostgres(ctx, s)
	case serviceClickHouse:
		s.ch, s.chAddr, err = startClickHouse(ctx, s)
	case serviceRedis:
		s.rdb, err = startRedis(ctx, s)
	case serviceMeilisearch:
		s.ms, err = startMeilisearch(ctx, s)
	case serviceKafka:
		s.kafkaSeed, s.kfk, err = startKafka(ctx, s)
	}

	if err != nil {
		s.startErrs[svc] = err
		t.Skipf("integration test skipped: failed to start %s testcontainer: %v", svc, err)
	}
	s.started[svc] = true
}

// Eventually polls fn every interval until it returns true or the timeout
// elapses, failing the test otherwise. Useful for asserting on async pipelines
// (Kafka consumers, search indexes).
func Eventually(t *testing.T, timeout, interval time.Duration, fn func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(interval)
	}
	t.Fatalf("condition not met within %s", timeout)
}

// WaitForRecord polls the consumer until a record matching pred is observed or
// the timeout elapses. It fails the test when no record matches.
func WaitForRecord(t *testing.T, client *kgo.Client, timeout time.Duration, pred func(*kgo.Record) bool) *kgo.Record {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		fetches := client.PollFetches(context.Background())
		if err := fetches.Err(); err != nil {
			t.Fatalf("poll fetches: %v", err)
		}

		for _, rec := range recordsFromFetches(fetches) {
			if pred(rec) {
				return rec
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no matching kafka record within %s", timeout)
	return nil
}

func recordsFromFetches(fetches kgo.Fetches) []*kgo.Record {
	var records []*kgo.Record
	fetches.EachRecord(func(rec *kgo.Record) {
		records = append(records, rec)
	})
	return records
}

// terminate stops every started container and closes clients.
func (s *suite) terminate(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, ctr := range s.containers {
		//nolint:errcheck // Best-effort container shutdown during test teardown.
		_ = ctr.Terminate(ctx)
	}
	s.containers = nil

	if s.pg != nil {
		s.pg.Close()
	}
	if s.ch != nil {
		_ = s.ch.Close()
	}
	if s.rdb != nil {
		_ = s.rdb.Close()
	}
	if s.kfk != nil {
		s.kfk.Close()
	}
}
