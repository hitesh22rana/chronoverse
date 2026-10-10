package commandidempotency_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestIntegrationSyncLegacyIdentitiesReplay(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	scope := commandidempotency.UserScope(uuid.NewString())
	operation := commandidempotency.WorkflowUpdateOperation("550e8400-e29b-41d4-a716-446655440000")
	const key = "replay-key"
	original := commandidempotency.LegacyIdentity{Operation: "update_workflow:550e8400-e29b-41d4-a716-446655440000", RequestHash: testHash(1)}
	tx := reserveTx(ctx, t, pg)
	defer tx.Rollback(ctx) //nolint:errcheck // Rollback is harmless after commit.
	_, reserveErr := commandidempotency.Reserve(ctx, tx, scope, operation, key, testHash(10), original.RequestHash)
	require.NoError(t, reserveErr)
	require.NoError(t, commandidempotency.SyncLegacyIdentities(ctx, tx, scope, operation, " "+key+" ", true, original, original))
	require.NoError(t, commandidempotency.Complete(ctx, tx, scope, operation, key, testHash(10), "workflow-1", map[string]string{"id": "workflow-1"}, time.Hour))
	require.NoError(t, tx.Commit(ctx))
	var before, after string
	query := "SELECT row_to_json(keys)::text FROM command_idempotency_keys keys WHERE scope=$1 AND operation=$2 AND idempotency_key=$3"
	require.NoError(t, pg.QueryRow(ctx, query, scope, operation, key).Scan(&before))
	tx = reserveTx(ctx, t, pg)
	defer tx.Rollback(ctx) //nolint:errcheck // Rollback is harmless after commit.
	replay, err := commandidempotency.Reserve(ctx, tx, scope, operation, key, original.RequestHash)
	if err != nil || !replay.Replay || replay.ResourceID != "workflow-1" {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	lexical := original
	lexical.RequestHash = testHash(2)
	alias := commandidempotency.LegacyIdentity{Operation: "update_workflow:550E8400-E29B-41D4-A716-446655440000", RequestHash: testHash(3)}
	require.NoError(t, commandidempotency.SyncLegacyIdentities(ctx, tx, scope, operation, key, false, lexical, alias))
	require.NoError(t, tx.Commit(ctx))
	require.NoError(t, pg.QueryRow(ctx, query, scope, operation, key).Scan(&after))
	if before != after {
		t.Fatalf("replay changed parent ledger: before=%s after=%s", before, after)
	}
	got := readLegacyIdentities(t, pg, scope, operation, key)
	want := map[string]string{original.Operation: original.RequestHash, alias.Operation: alias.RequestHash}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("identities=%v want=%v", got, want)
	}
	tx = reserveTx(ctx, t, pg)
	defer tx.Rollback(ctx) //nolint:errcheck // Rollback is harmless after commit.
	require.NoError(t, commandidempotency.SyncLegacyIdentities(ctx, tx, scope, operation, key, true, alias))
	require.NoError(t, tx.Commit(ctx))
	if got = readLegacyIdentities(t, pg, scope, operation, key); !reflect.DeepEqual(got, map[string]string{alias.Operation: alias.RequestHash}) {
		t.Fatalf("fresh replacement=%v", got)
	}
}

func TestIntegrationSyncLegacyIdentitiesRollback(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		for _, conflict := range []bool{false, true} {
			name := "replay-invalid"
			if fresh {
				name = "fresh-invalid"
			}
			if conflict {
				name += "-conflict"
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				pg := testkit.Postgres(t)
				scope := commandidempotency.UserScope(uuid.NewString())
				operation := commandidempotency.WorkflowUpdateOperation("550e8400-e29b-41d4-a716-446655440000")
				const key = "rollback-key"
				original := commandidempotency.LegacyIdentity{Operation: "update_workflow:550e8400-e29b-41d4-a716-446655440000", RequestHash: testHash(1)}
				tx := reserveTx(ctx, t, pg)
				defer tx.Rollback(ctx) //nolint:errcheck // Rollback is harmless after commit.
				_, reserveErr := commandidempotency.Reserve(ctx, tx, scope, operation, key, testHash(10))
				require.NoError(t, reserveErr)
				require.NoError(t, commandidempotency.SyncLegacyIdentities(ctx, tx, scope, operation, key, true, original))
				require.NoError(t, commandidempotency.Complete(ctx, tx, scope, operation, key, testHash(10), "workflow-1", nil, time.Hour))
				require.NoError(t, tx.Commit(ctx))
				tx = reserveTx(ctx, t, pg)
				defer tx.Rollback(ctx) //nolint:errcheck // Covers failures before explicit rollback.
				first := commandidempotency.LegacyIdentity{Operation: "update_workflow:550E8400-E29B-41D4-A716-446655440000", RequestHash: testHash(2)}
				bad := commandidempotency.LegacyIdentity{Operation: "invalid", RequestHash: "invalid"}
				if conflict {
					bad = first
					bad.RequestHash = testHash(3)
				}
				err := commandidempotency.SyncLegacyIdentities(ctx, tx, scope, operation, key, fresh, first, bad)
				if status.Code(err) != codes.Internal {
					t.Fatalf("error=%v want Internal", err)
				}
				var inserted int
				queryErr := tx.QueryRow(ctx, `SELECT count(*) FROM command_idempotency_legacy_identities
WHERE scope=$1 AND operation=$2 AND idempotency_key=$3 AND legacy_operation=$4`, scope, operation, key, first.Operation).Scan(&inserted)
				require.NoError(t, queryErr)
				require.Equal(t, 1, inserted)
				require.NoError(t, tx.Rollback(ctx))
				if got := readLegacyIdentities(t, pg, scope, operation, key); !reflect.DeepEqual(got, map[string]string{original.Operation: original.RequestHash}) {
					t.Fatalf("rollback identities=%v", got)
				}
				replay, err := tryReserve(ctx, t, pg, scope, operation, key, testHash(10))
				if err != nil || !replay.Replay {
					t.Fatalf("parent replay=%+v error=%v", replay, err)
				}
			})
		}
	}
}

func readLegacyIdentities(t *testing.T, pg *postgres.Postgres, scope, operation, key string) map[string]string {
	t.Helper()
	rows, err := pg.Query(context.Background(), `SELECT legacy_operation, legacy_request_hash FROM command_idempotency_legacy_identities
WHERE scope=$1 AND operation=$2 AND idempotency_key=$3`, scope, operation, key)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var operation, hash string
		require.NoError(t, rows.Scan(&operation, &hash))
		result[operation] = hash
	}
	require.NoError(t, rows.Err())
	return result
}
