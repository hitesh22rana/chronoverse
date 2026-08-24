//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package runtime

import (
	"context"
	"testing"
	"time"

	runtimemodel "github.com/hitesh22rana/chronoverse/internal/model/runtime"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestMain(m *testing.M) {
	testkit.Run(m, testkit.WithPostgres())
}

//nolint:gocyclo // Linear lifecycle walk over many states; splitting it would obscure the sequence.
func TestIntegrationRuntimeNodeLifecycle(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := New(Config{
		ID:             "node-" + t.Name(),
		NodeName:       "test-node-" + t.Name(),
		DockerEndpoint: "tcp://docker-proxy:2375",
		MaxConcurrency: 4,
	}, pg)

	// RegisterReady upserts the node as READY with a fresh heartbeat.
	if err := repo.RegisterReady(ctx); err != nil {
		t.Fatalf("RegisterReady: %v", err)
	}

	var status string
	var maxConcurrency int32
	if err := pg.QueryRow(ctx, `SELECT status, max_concurrency FROM runtime_nodes WHERE id = $1`, repo.cfg.ID).Scan(&status, &maxConcurrency); err != nil {
		t.Fatalf("fetch runtime node: %v", err)
	}
	if status != string(runtimemodel.NodeStatusReady) {
		t.Fatalf("status = %q, want %q", status, runtimemodel.NodeStatusReady)
	}
	if maxConcurrency != 4 {
		t.Fatalf("max_concurrency = %d, want 4", maxConcurrency)
	}

	// RegisterReady is idempotent (upsert).
	if err := repo.RegisterReady(ctx); err != nil {
		t.Fatalf("RegisterReady (replay): %v", err)
	}

	// Heartbeat keeps the node READY and refreshes the heartbeat.
	before := time.Now().UTC().Add(-time.Minute)
	if _, err := pg.Exec(ctx, `UPDATE runtime_nodes SET last_heartbeat_at = $1 WHERE id = $2`, before, repo.cfg.ID); err != nil {
		t.Fatalf("backdate heartbeat: %v", err)
	}
	if err := repo.Heartbeat(ctx); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	var heartbeat time.Time
	if err := pg.QueryRow(ctx, `SELECT last_heartbeat_at FROM runtime_nodes WHERE id = $1`, repo.cfg.ID).Scan(&heartbeat); err != nil {
		t.Fatalf("fetch heartbeat: %v", err)
	}
	if heartbeat.Before(before) {
		t.Fatalf("heartbeat not refreshed: %v before %v", heartbeat, before)
	}

	// MarkDraining transitions the node to DRAINING.
	if err := repo.MarkDraining(ctx); err != nil {
		t.Fatalf("MarkDraining: %v", err)
	}
	if err := pg.QueryRow(ctx, `SELECT status FROM runtime_nodes WHERE id = $1`, repo.cfg.ID).Scan(&status); err != nil {
		t.Fatalf("fetch draining status: %v", err)
	}
	if status != string(runtimemodel.NodeStatusDraining) {
		t.Fatalf("status = %q, want %q", status, runtimemodel.NodeStatusDraining)
	}

	// MarkUnhealthy transitions the node to UNHEALTHY.
	if err := repo.MarkUnhealthy(ctx); err != nil {
		t.Fatalf("MarkUnhealthy: %v", err)
	}
	if err := pg.QueryRow(ctx, `SELECT status FROM runtime_nodes WHERE id = $1`, repo.cfg.ID).Scan(&status); err != nil {
		t.Fatalf("fetch unhealthy status: %v", err)
	}
	if status != string(runtimemodel.NodeStatusUnhealthy) {
		t.Fatalf("status = %q, want %q", status, runtimemodel.NodeStatusUnhealthy)
	}
}
