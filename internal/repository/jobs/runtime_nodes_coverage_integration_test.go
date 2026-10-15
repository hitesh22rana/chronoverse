//nolint:testpackage // Uses repository constructors with isolated PostgreSQL storage.
package jobs

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func runtimeSelectionRepository(t *testing.T) *Repository {
	t.Helper()
	ctx := context.Background()
	admin := testkit.Postgres(t)
	database := "runtime_selection_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+database); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(ctx, "DROP DATABASE "+database+" WITH (FORCE)"); err != nil {
			t.Errorf("drop fixture database: %v", err)
		}
	})
	cfg, err := pgxpool.ParseConfig(testkit.PostgresDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	pg, err := postgres.New(ctx, &postgres.Config{
		Host: cfg.ConnConfig.Host, Port: int(cfg.ConnConfig.Port),
		User: cfg.ConnConfig.User, Password: cfg.ConnConfig.Password,
		Database: database, MaxConns: 2, MinConns: 1,
		TLSConfig: &postgres.TLSConfig{Enabled: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pg.Close)
	if _, err := pg.Exec(ctx, `CREATE TABLE runtime_nodes (
 id TEXT PRIMARY KEY, docker_endpoint TEXT NOT NULL, status TEXT NOT NULL,
 last_heartbeat_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
 running_jobs INTEGER NOT NULL CHECK (running_jobs >= 0))`); err != nil {
		t.Fatal(err)
	}
	return New(&Config{RuntimeHeartbeatTTL: time.Minute}, nil, pg, nil, nil, nil, nil)
}

func TestIntegrationListReadyRuntimeNodesFiltersAndOrders(t *testing.T) {
	repo := runtimeSelectionRepository(t)
	ctx := context.Background()
	// PostgreSQL timestamps keep freshness and heartbeat ties independent of host clocks.
	for _, node := range []struct {
		id, state string
		jobs, age int
	}{
		{"busy", "READY", 9, 1},
		{"tie-b", "READY", 0, 5},
		{"older", "READY", 0, 15},
		{"tie-a", "READY", 0, 5},
		{"stale", "READY", 0, 120},
		{"draining", "DRAINING", 0, 1},
		{"unhealthy", "UNHEALTHY", 0, 1},
	} {
		if _, err := repo.pg.Exec(ctx, `INSERT INTO runtime_nodes VALUES ($1,$2,$3,
   (now() AT TIME ZONE 'utc') - ($5::int * interval '1 second'),$4)`,
			node.id, "tcp://"+node.id+":2375", node.state, node.jobs, node.age); err != nil {
			t.Fatal(err)
		}
	}
	// Set tied/ordered heartbeats relative to one database instant, safely within the TTL.
	if _, err := repo.pg.Exec(ctx, `UPDATE runtime_nodes SET last_heartbeat_at =
  (now() AT TIME ZONE 'utc') - CASE id WHEN 'stale' THEN interval '120 seconds'
  WHEN 'older' THEN interval '15 seconds' WHEN 'tie-a' THEN interval '5 seconds'
  WHEN 'tie-b' THEN interval '5 seconds' ELSE interval '1 second' END`); err != nil {
		t.Fatal(err)
	}
	nodes, err := repo.ListReadyRuntimeNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []*jobsmodel.RuntimeNode{
		{RuntimeNodeID: "tie-a", RuntimeEndpoint: "tcp://tie-a:2375"},
		{RuntimeNodeID: "tie-b", RuntimeEndpoint: "tcp://tie-b:2375"},
		{RuntimeNodeID: "older", RuntimeEndpoint: "tcp://older:2375"},
		{RuntimeNodeID: "busy", RuntimeEndpoint: "tcp://busy:2375"},
	}
	if !slices.EqualFunc(nodes, want, func(a, b *jobsmodel.RuntimeNode) bool { return *a == *b }) {
		t.Fatalf("nodes=%+v, want %+v", nodes, want)
	}
	// A shorter configured TTL excludes nodes that the default TTL accepted.
	repo.cfg.RuntimeHeartbeatTTL = 10 * time.Second
	nodes, err = repo.ListReadyRuntimeNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 3 || nodes[0].RuntimeNodeID != "tie-a" || nodes[1].RuntimeNodeID != "tie-b" || nodes[2].RuntimeNodeID != "busy" {
		t.Fatalf("short TTL nodes=%+v", nodes)
	}
	assertRuntimeSelectionOrder(t, repo, want)
}

func assertRuntimeSelectionOrder(t *testing.T, repo *Repository, want []*jobsmodel.RuntimeNode) {
	t.Helper()
	ctx := context.Background()
	// Selecting and removing each winner proves LIMIT1 uses the same full ordering.
	repo.cfg.RuntimeHeartbeatTTL = time.Minute
	for _, expected := range want {
		node, selectErr := repo.GetReadyRuntimeNode(ctx)
		if selectErr != nil || node == nil || *node != *expected {
			t.Fatalf("selected node=%+v error=%v, want %+v", node, selectErr, expected)
		}
		if _, updateErr := repo.pg.Exec(ctx, `UPDATE runtime_nodes SET status = 'DRAINING' WHERE id = $1`, expected.RuntimeNodeID); updateErr != nil {
			t.Fatal(updateErr)
		}
	}
	node, selectErr := repo.GetReadyRuntimeNode(ctx)
	if node != nil || status.Code(selectErr) != codes.Unavailable || status.Convert(selectErr).Message() != "no healthy runtime node is available" {
		t.Fatalf("exhausted selection node=%+v error=%v; want Unavailable", node, selectErr)
	}
}

func TestIntegrationListReadyRuntimeNodesEmptyAndContextErrors(t *testing.T) {
	repo := runtimeSelectionRepository(t)
	nodes, err := repo.ListReadyRuntimeNodes(context.Background())
	if err != nil || len(nodes) != 0 {
		t.Fatalf("empty nodes=%v error=%v", nodes, err)
	}
	node, selectErr := repo.GetReadyRuntimeNode(context.Background())
	if node != nil || status.Code(selectErr) != codes.Unavailable {
		t.Fatalf("empty selection node=%v error=%v; want Unavailable", node, selectErr)
	}
	for _, tc := range []struct {
		name string
		code codes.Code
	}{{"canceled", codes.Canceled}, {"expired", codes.DeadlineExceeded}} {
		t.Run(tc.name, func(t *testing.T) {
			var ctx context.Context
			var cancel context.CancelFunc
			if tc.code == codes.Canceled {
				ctx, cancel = context.WithCancel(context.Background())
			} else {
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			}
			cancel()
			nodes, err := repo.ListReadyRuntimeNodes(ctx)
			if nodes != nil || status.Code(err) != tc.code {
				t.Fatalf("nodes=%v error=%v, want %v", nodes, err, tc.code)
			}
			node, selectErr := repo.GetReadyRuntimeNode(ctx)
			if node != nil || status.Code(selectErr) != tc.code {
				t.Fatalf("selected node=%v error=%v; want %v", node, selectErr, tc.code)
			}
		})
	}
}

func TestIntegrationListReadyRuntimeNodesStorageFailures(t *testing.T) {
	for _, tc := range []struct{ name, setup string }{
		{"query", `DROP TABLE runtime_nodes`},
		{"scan", `ALTER TABLE runtime_nodes ALTER COLUMN docker_endpoint DROP NOT NULL;
   INSERT INTO runtime_nodes VALUES ('broken',NULL,'READY',now() AT TIME ZONE 'utc',0)`},
		{"iteration", `DROP TABLE runtime_nodes;
   CREATE VIEW runtime_nodes AS SELECT 'broken'::text id,
    (1 / (extract(epoch FROM clock_timestamp())::bigint % 1))::text docker_endpoint,
    'READY'::text status, now() AT TIME ZONE 'utc' last_heartbeat_at, 0 running_jobs`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := runtimeSelectionRepository(t)
			if _, err := repo.pg.Exec(context.Background(), tc.setup); err != nil {
				t.Fatal(err)
			}
			nodes, err := repo.ListReadyRuntimeNodes(context.Background())
			if nodes != nil || status.Code(err) != codes.Internal || !strings.Contains(status.Convert(err).Message(), "list ready runtime nodes") {
				t.Fatalf("nodes=%v error=%v, want contextual Internal", nodes, err)
			}
			node, selectErr := repo.GetReadyRuntimeNode(context.Background())
			if node != nil || status.Code(selectErr) != codes.Internal || !strings.Contains(status.Convert(selectErr).Message(), "get ready runtime node") {
				t.Fatalf("selection node=%v error=%v; want contextual Internal", node, selectErr)
			}
		})
	}
}
