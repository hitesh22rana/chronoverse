//nolint:testpackage // Exercises repository transactions against PostgreSQL.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

// runtimeNodeFailureMessage is raised by the fault triggers installed through
// installRuntimeNodeFailure; assertions look for it to prove the database, not
// the repository, produced the error under test.
const runtimeNodeFailureMessage = "runtime node test failure"

// heartbeatTag returns a fresh identity fragment. Fixtures never derive their
// whole identity from t.Name(): repeated runs of the same test binary must not
// collide on runtime node ids or the unique users email.
func heartbeatTag() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}

func runtimeRepository(t *testing.T) *Repository {
	t.Helper()
	return New(Config{
		ID:             "node-" + t.Name() + "-" + heartbeatTag(),
		NodeName:       "host-" + t.Name() + "-" + heartbeatTag(),
		DockerEndpoint: "tcp://docker:2375",
		MaxConcurrency: 4,
	}, testkit.Postgres(t))
}

func TestIntegrationHeartbeatRegistersMissingNode(t *testing.T) {
	repo := runtimeRepository(t)
	ctx := t.Context()
	if err := repo.Heartbeat(ctx); err != nil {
		t.Fatalf("register missing node: %v", err)
	}
	var name, endpoint, nodeStatus string
	var concurrency, running int
	var fresh bool
	if err := repo.pg.QueryRow(ctx, `SELECT node_name, docker_endpoint, status, max_concurrency, running_jobs,
		last_heartbeat_at > now() AT TIME ZONE 'utc' - interval '1 minute' FROM runtime_nodes WHERE id=$1`, repo.cfg.ID).
		Scan(&name, &endpoint, &nodeStatus, &concurrency, &running, &fresh); err != nil {
		t.Fatal(err)
	}
	if name != repo.cfg.NodeName || endpoint != repo.cfg.DockerEndpoint || nodeStatus != "READY" || concurrency != 4 || running != 0 || !fresh {
		t.Fatalf("registered node = %q/%q/%q/%d/%d/fresh:%v", name, endpoint, nodeStatus, concurrency, running, fresh)
	}
	if err := repo.Heartbeat(ctx); err != nil {
		t.Fatalf("heartbeat registered node again: %v", err)
	}
}

func TestIntegrationHeartbeatRegistrationConflictPreservesOwner(t *testing.T) {
	repo := runtimeRepository(t)
	ctx := t.Context()
	if err := repo.RegisterReady(ctx); err != nil {
		t.Fatal(err)
	}
	before := readNodeState(t, repo)
	other := New(repo.cfg, repo.pg)
	other.cfg.ID += "-conflict"
	err := other.Heartbeat(ctx)
	if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "failed to upsert runtime node") ||
		!strings.Contains(err.Error(), "idx_runtime_nodes_node_name") {
		t.Fatalf("name collision = %v, want Internal upsert error on the node-name index", err)
	}
	var count int
	if err := repo.pg.QueryRow(ctx, `SELECT count(*) FROM runtime_nodes WHERE node_name=$1 AND id=$2`, repo.cfg.NodeName, repo.cfg.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("existing owner count=%d, error=%v", count, err)
	}
	if err := repo.pg.QueryRow(ctx, `SELECT count(*) FROM runtime_nodes WHERE id=$1`, other.cfg.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("conflicting node count=%d, error=%v", count, err)
	}
	if after := readNodeState(t, repo); after != before {
		t.Fatalf("registration conflict changed owner: before=%+v after=%+v", before, after)
	}
}

func TestIntegrationHeartbeatReconcilesOnlyOwnedRunningJobs(t *testing.T) {
	repo := runtimeRepository(t)
	other := runtimeRepository(t)
	ctx := t.Context()
	for _, node := range []*Repository{repo, other} {
		if err := node.RegisterReady(ctx); err != nil {
			t.Fatal(err)
		}
	}
	user := testkit.SeedUser(ctx, t, repo.pg, "heartbeat-"+heartbeatTag()+"@example.test")
	workflow := testkit.SeedWorkflow(ctx, t, repo.pg, user, t.Name())
	if _, err := repo.pg.Exec(ctx, `INSERT INTO jobs (workflow_id,user_id,status,scheduled_at,runtime_node_id,trigger)
		VALUES ($1,$2,'RUNNING',now(),$3,'MANUAL'),($1,$2,'RUNNING',now(),$3,'MANUAL'),($1,$2,'COMPLETED',now(),$3,'MANUAL'),
		($1,$2,'RUNNING',now(),$4,'MANUAL'),($1,$2,'RUNNING',now(),NULL,'MANUAL')`, workflow, user, repo.cfg.ID, other.cfg.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.pg.Exec(ctx, `UPDATE runtime_nodes SET running_jobs=17,status='UNHEALTHY',last_heartbeat_at='2000-01-01' WHERE id IN ($1,$2)`, repo.cfg.ID, other.cfg.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.Heartbeat(ctx); err != nil {
		t.Fatal(err)
	}
	var running int
	var ready bool
	if err := repo.pg.QueryRow(ctx, `SELECT running_jobs,status='READY' AND last_heartbeat_at > '2000-01-01' FROM runtime_nodes WHERE id=$1`, repo.cfg.ID).
		Scan(&running, &ready); err != nil || running != 2 || !ready {
		t.Fatalf("reconciled running=%d ready=%v error=%v", running, ready, err)
	}
	if err := repo.pg.QueryRow(ctx, `SELECT running_jobs,status='UNHEALTHY' AND last_heartbeat_at='2000-01-01' FROM runtime_nodes WHERE id=$1`, other.cfg.ID).
		Scan(&running, &ready); err != nil || running != 17 || !ready {
		t.Fatalf("other node changed: running=%d unchanged=%v error=%v", running, ready, err)
	}
}

type nodeState struct {
	status    string
	running   int
	heartbeat time.Time
	updated   time.Time
}

func readNodeState(t *testing.T, repo *Repository) nodeState {
	t.Helper()
	var s nodeState
	if err := repo.pg.QueryRow(t.Context(), `SELECT status,running_jobs,last_heartbeat_at,updated_at FROM runtime_nodes WHERE id=$1`, repo.cfg.ID).
		Scan(&s.status, &s.running, &s.heartbeat, &s.updated); err != nil {
		t.Fatal(err)
	}
	return s
}

// markNodeStaleAndUnhealthy backdates a registered node and inflates its
// running count so a successful write is observable.
func markNodeStaleAndUnhealthy(t *testing.T, repo *Repository) {
	t.Helper()
	if _, err := repo.pg.Exec(t.Context(), `UPDATE runtime_nodes
		SET status='UNHEALTHY',running_jobs=17,last_heartbeat_at='2000-01-01',updated_at='2000-01-01' WHERE id=$1`, repo.cfg.ID); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationHeartbeatTransactionFailuresRollback(t *testing.T) {
	for _, tc := range []struct {
		name, clause, condition, prefix string
	}{
		{"update", "BEFORE UPDATE ON runtime_nodes", "NEW.status='READY'", "failed to heartbeat runtime node"},
		{"reconcile", "BEFORE UPDATE ON runtime_nodes", "NEW.running_jobs <> OLD.running_jobs", "failed to reconcile runtime running jobs"},
		{"commit", "AFTER UPDATE ON runtime_nodes DEFERRABLE INITIALLY DEFERRED", "TRUE", "failed to commit runtime heartbeat transaction"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := runtimeRepository(t)
			ctx := t.Context()
			if err := repo.RegisterReady(ctx); err != nil {
				t.Fatal(err)
			}
			markNodeStaleAndUnhealthy(t, repo)
			before := readNodeState(t, repo)
			remove := installRuntimeNodeFailure(t, repo.pg, repo.cfg.ID, tc.clause, tc.condition)
			err := repo.Heartbeat(ctx)
			if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), tc.prefix) || !strings.Contains(err.Error(), runtimeNodeFailureMessage) {
				t.Fatalf("Heartbeat=%v, want Internal %q with database cause", err, tc.prefix)
			}
			if after := readNodeState(t, repo); after != before {
				t.Fatalf("failed transaction changed node: before=%+v after=%+v", before, after)
			}
			remove()
			if err := repo.Heartbeat(ctx); err != nil {
				t.Fatalf("retry after failure: %v", err)
			}
			after := readNodeState(t, repo)
			if after.status != "READY" || after.running != 0 || !after.heartbeat.After(before.heartbeat) {
				t.Fatalf("retry failed to reconcile node: %+v", after)
			}
		})
	}
}

func TestIntegrationRegisterReadyTransactionFailuresRollback(t *testing.T) {
	for _, tc := range []struct {
		name, clause, condition, prefix string
	}{
		{"reconcile", "BEFORE UPDATE ON runtime_nodes", "NEW.running_jobs <> OLD.running_jobs", "failed to reconcile runtime running jobs"},
		{"commit", "AFTER UPDATE ON runtime_nodes DEFERRABLE INITIALLY DEFERRED", "TRUE", "failed to commit runtime upsert transaction"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := runtimeRepository(t)
			ctx := t.Context()
			if err := repo.RegisterReady(ctx); err != nil {
				t.Fatal(err)
			}
			markNodeStaleAndUnhealthy(t, repo)
			before := readNodeState(t, repo)
			remove := installRuntimeNodeFailure(t, repo.pg, repo.cfg.ID, tc.clause, tc.condition)
			err := repo.RegisterReady(ctx)
			if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), tc.prefix) || !strings.Contains(err.Error(), runtimeNodeFailureMessage) {
				t.Fatalf("RegisterReady=%v, want Internal %q with database cause", err, tc.prefix)
			}
			if after := readNodeState(t, repo); after != before {
				t.Fatalf("failed registration changed node: before=%+v after=%+v", before, after)
			}
			remove()
			if err := repo.RegisterReady(ctx); err != nil {
				t.Fatalf("retry after failure: %v", err)
			}
			after := readNodeState(t, repo)
			if after.status != "READY" || after.running != 0 || !after.heartbeat.After(before.heartbeat) {
				t.Fatalf("retry failed to register node: %+v", after)
			}
		})
	}
}

func TestIntegrationRegisterReadyCanceledBeforeBegin(t *testing.T) {
	repo := runtimeRepository(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := repo.RegisterReady(ctx)
	if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "failed to start runtime upsert transaction") {
		t.Fatalf("canceled registration=%v", err)
	}
	var count int
	if err := repo.pg.QueryRow(t.Context(), `SELECT count(*) FROM runtime_nodes WHERE id=$1`, repo.cfg.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("canceled registration created node: count=%d err=%v", count, err)
	}
}

func TestIntegrationRegisterReadyMissingRowAfterInsert(t *testing.T) {
	repo := runtimeRepository(t)
	ctx := t.Context()
	remove := installInsertSuppression(t, repo.pg, repo.cfg.ID)
	err := repo.RegisterReady(ctx)
	if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "failed to lock runtime node after upsert") ||
		!strings.Contains(err.Error(), pgx.ErrNoRows.Error()) {
		t.Fatalf("registration with suppressed insert = %v, want post-upsert lock error", err)
	}
	var count int
	if err := repo.pg.QueryRow(ctx, `SELECT count(*) FROM runtime_nodes WHERE id=$1`, repo.cfg.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("suppressed insert created node: count=%d err=%v", count, err)
	}
	remove()
	if err := repo.RegisterReady(ctx); err != nil {
		t.Fatalf("registration after removing suppression: %v", err)
	}
	if state := readNodeState(t, repo); state.status != "READY" || state.running != 0 {
		t.Fatalf("registered node state = %+v", state)
	}
}

// installRuntimeNodeFailure injects a statement or commit failure targeting the
// given node id without changing table types or row validity.
func installRuntimeNodeFailure(t *testing.T, pg *postgres.Postgres, id, clause, condition string) func() {
	t.Helper()
	constraint := ""
	if strings.Contains(clause, "DEFERRABLE") {
		constraint = "CONSTRAINT "
	}
	ddl := fmt.Sprintf(`CREATE FUNCTION test_runtime_node_failure() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION '%s'; END; $$ LANGUAGE plpgsql;
		CREATE %sTRIGGER test_runtime_node_failure_trigger %s FOR EACH ROW
		WHEN (NEW.id = '%s' AND (%s)) EXECUTE FUNCTION test_runtime_node_failure();`, runtimeNodeFailureMessage, constraint, clause, strings.ReplaceAll(id, "'", "''"), condition)
	if _, err := pg.Exec(t.Context(), ddl); err != nil {
		t.Fatalf("install failure: %v", err)
	}
	remove := func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		if _, err := pg.Exec(ctx, `DROP TRIGGER IF EXISTS test_runtime_node_failure_trigger ON runtime_nodes; DROP FUNCTION IF EXISTS test_runtime_node_failure();`); err != nil {
			t.Errorf("remove failure trigger: %v", err)
		}
	}
	t.Cleanup(remove)
	return remove
}

// installInsertSuppression drops the targeted INSERT so the upsert reports
// success without writing a row, exercising the post-upsert missing-row guard.
func installInsertSuppression(t *testing.T, pg *postgres.Postgres, id string) func() {
	t.Helper()
	ddl := fmt.Sprintf(`CREATE FUNCTION test_runtime_insert_suppression() RETURNS trigger AS $$ BEGIN RETURN NULL; END; $$ LANGUAGE plpgsql;
		CREATE TRIGGER test_runtime_insert_suppression_trigger BEFORE INSERT ON runtime_nodes
		FOR EACH ROW WHEN (NEW.id = '%s') EXECUTE FUNCTION test_runtime_insert_suppression();`, strings.ReplaceAll(id, "'", "''"))
	if _, err := pg.Exec(t.Context(), ddl); err != nil {
		t.Fatalf("install insert suppression: %v", err)
	}
	remove := func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		if _, err := pg.Exec(ctx, `DROP TRIGGER IF EXISTS test_runtime_insert_suppression_trigger ON runtime_nodes; DROP FUNCTION IF EXISTS test_runtime_insert_suppression();`); err != nil {
			t.Errorf("remove insert suppression: %v", err)
		}
	}
	t.Cleanup(remove)
	return remove
}

func TestIntegrationHeartbeatCanceledBeforeBegin(t *testing.T) {
	repo := runtimeRepository(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := repo.Heartbeat(ctx)
	if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "failed to start runtime heartbeat transaction") {
		t.Fatalf("canceled heartbeat=%v", err)
	}
	var count int
	if err := repo.pg.QueryRow(t.Context(), `SELECT count(*) FROM runtime_nodes WHERE id=$1`, repo.cfg.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("canceled heartbeat created node: count=%d err=%v", count, err)
	}
}

func TestIntegrationHeartbeatCanceledWhileWaitingForNodeLock(t *testing.T) {
	repo := runtimeRepository(t)
	if err := repo.RegisterReady(t.Context()); err != nil {
		t.Fatal(err)
	}
	before := readNodeState(t, repo)
	tx, err := repo.pg.BeginTx(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cleanupCancel()
		if err := tx.Rollback(cleanupCtx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Errorf("cleanup locked transaction: %v", err)
		}
	}()
	var pid int
	if err := tx.QueryRow(t.Context(), `SELECT pg_backend_pid() FROM runtime_nodes WHERE id=$1 FOR UPDATE`, repo.cfg.ID).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- repo.Heartbeat(ctx) }()
	waitForHeartbeatLock(t, repo.pg, pid)
	cancel()
	select {
	case err := <-result:
		if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "failed to lock runtime node for heartbeat") {
			t.Fatalf("blocked heartbeat=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("heartbeat did not exit after cancellation")
	}
	if err := tx.Rollback(t.Context()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		t.Fatal(err)
	}
	if after := readNodeState(t, repo); after != before {
		t.Fatalf("canceled heartbeat changed node: before=%+v after=%+v", before, after)
	}
	if err := repo.Heartbeat(t.Context()); err != nil {
		t.Fatalf("heartbeat after releasing lock: %v", err)
	}
}

func waitForHeartbeatLock(t *testing.T, pg *postgres.Postgres, blocker int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := pg.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid)))`, blocker).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("heartbeat never waited on the held node lock")
		}
	}
}
