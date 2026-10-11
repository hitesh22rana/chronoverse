package commandidempotency_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

// rowQuerier reads committed or transactional ledger state.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func TestIntegrationSyncLegacyIdentitiesReplay(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	scope := commandidempotency.UserScope(uuid.NewString())
	operation := commandidempotency.WorkflowUpdateOperation("550e8400-e29b-41d4-a716-446655440000")
	const key = "replay-key"
	original := commandidempotency.LegacyIdentity{Operation: "update_workflow:550e8400-e29b-41d4-a716-446655440000", RequestHash: testHash(1)}

	// A fresh reservation is PROCESSING, so its legacy identities reset first.
	tx := reserveTx(ctx, t, pg)
	defer tx.Rollback(ctx) //nolint:errcheck // Rollback is harmless after commit.
	reservation, reserveErr := commandidempotency.Reserve(ctx, tx, scope, operation, key, testHash(10), original.RequestHash)
	require.NoError(t, reserveErr)
	require.False(t, reservation.Replay)
	require.Equal(t, "PROCESSING", parentLedgerStatus(ctx, t, tx, scope, operation, key))
	require.NoError(t, commandidempotency.SyncLegacyIdentities(ctx, tx, scope, operation, key, !reservation.Replay, original, original))
	require.NoError(t, commandidempotency.Complete(ctx, tx, scope, operation, key, testHash(10), "workflow-1", map[string]string{"id": "workflow-1"}, commandidempotency.ClientCommandRetention))
	require.NoError(t, tx.Commit(ctx))
	before := parentLedgerRow(ctx, t, pg, scope, operation, key)

	// The stored legacy hash replays the completed command.
	tx = reserveTx(ctx, t, pg)
	defer tx.Rollback(ctx) //nolint:errcheck // Rollback is harmless after commit.
	replay, err := commandidempotency.Reserve(ctx, tx, scope, operation, key, original.RequestHash)
	if err != nil || !replay.Replay || replay.ResourceID != "workflow-1" {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	require.Equal(t, "COMPLETED", parentLedgerStatus(ctx, t, tx, scope, operation, key))

	// Replay adds UUID spellings, preserving the original lexical hash and parent.
	lexical := original
	lexical.RequestHash = testHash(2)
	alias := commandidempotency.LegacyIdentity{Operation: "update_workflow:550E8400-E29B-41D4-A716-446655440000", RequestHash: testHash(3)}
	require.NoError(t, commandidempotency.SyncLegacyIdentities(ctx, tx, scope, operation, key, !replay.Replay, lexical, alias))
	require.NoError(t, tx.Commit(ctx))
	if after := parentLedgerRow(ctx, t, pg, scope, operation, key); before != after {
		t.Fatalf("replay changed parent ledger: before=%s after=%s", before, after)
	}
	got := readLegacyIdentities(ctx, t, pg, scope, operation, key)
	want := map[string]string{original.Operation: original.RequestHash, alias.Operation: alias.RequestHash}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("identities=%v want=%v", got, want)
	}

	// Expiry allows a fresh PROCESSING reservation to replace the identities.
	expireKey(ctx, t, pg, scope, operation, key)
	tx = reserveTx(ctx, t, pg)
	defer tx.Rollback(ctx) //nolint:errcheck // Rollback is harmless after commit.
	replacement, err := commandidempotency.Reserve(ctx, tx, scope, operation, key, testHash(10))
	require.NoError(t, err)
	require.False(t, replacement.Replay)
	require.Equal(t, "PROCESSING", parentLedgerStatus(ctx, t, tx, scope, operation, key))
	require.NoError(t, commandidempotency.SyncLegacyIdentities(ctx, tx, scope, operation, key, !replacement.Replay, alias))
	require.NoError(t, tx.Commit(ctx))
	if got = readLegacyIdentities(ctx, t, pg, scope, operation, key); !reflect.DeepEqual(got, map[string]string{alias.Operation: alias.RequestHash}) {
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

				// Establish the committed baseline the rollback must restore.
				tx := reserveTx(ctx, t, pg)
				defer tx.Rollback(ctx) //nolint:errcheck // Rollback is harmless after commit.
				reservation, reserveErr := commandidempotency.Reserve(ctx, tx, scope, operation, key, testHash(10))
				require.NoError(t, reserveErr)
				require.False(t, reservation.Replay)
				require.NoError(t, commandidempotency.SyncLegacyIdentities(ctx, tx, scope, operation, key, !reservation.Replay, original))
				require.NoError(t, commandidempotency.Complete(ctx, tx, scope, operation, key, testHash(10), "workflow-1", nil, commandidempotency.ClientCommandRetention))
				require.NoError(t, tx.Commit(ctx))
				before := parentLedgerRow(ctx, t, pg, scope, operation, key)

				// Reserve the matching fresh or replay state within the rollback transaction.
				tx = reserveTx(ctx, t, pg)
				defer tx.Rollback(ctx) //nolint:errcheck // Covers failures before explicit rollback.
				var err error
				if fresh {
					expireKeyInTx(ctx, t, tx, scope, operation, key)
					reservation, err = commandidempotency.Reserve(ctx, tx, scope, operation, key, testHash(10))
					require.NoError(t, err)
					require.False(t, reservation.Replay)
					require.Equal(t, "PROCESSING", parentLedgerStatus(ctx, t, tx, scope, operation, key))
				} else {
					reservation, err = commandidempotency.Reserve(ctx, tx, scope, operation, key, testHash(10))
					require.NoError(t, err)
					require.True(t, reservation.Replay)
					require.Equal(t, "workflow-1", reservation.ResourceID)
					require.Equal(t, "COMPLETED", parentLedgerStatus(ctx, t, tx, scope, operation, key))
				}

				first := commandidempotency.LegacyIdentity{Operation: "update_workflow:550E8400-E29B-41D4-A716-446655440000", RequestHash: testHash(2)}
				bad := commandidempotency.LegacyIdentity{Operation: "invalid", RequestHash: "invalid"}
				if conflict {
					bad = first
					bad.RequestHash = testHash(3)
				}
				err = commandidempotency.SyncLegacyIdentities(ctx, tx, scope, operation, key, !reservation.Replay, first, bad)
				if status.Code(err) != codes.Internal {
					t.Fatalf("error=%v want Internal", err)
				}

				// Rejection follows a partial identity write that rollback must undo.
				var inserted int
				queryErr := tx.QueryRow(ctx, `SELECT count(*) FROM command_idempotency_legacy_identities
WHERE scope=$1 AND operation=$2 AND idempotency_key=$3 AND legacy_operation=$4`, scope, operation, key, first.Operation).Scan(&inserted)
				require.NoError(t, queryErr)
				require.Equal(t, 1, inserted)
				require.NoError(t, tx.Rollback(ctx))

				// Rollback restores the completed parent and original identities.
				if after := parentLedgerRow(ctx, t, pg, scope, operation, key); before != after {
					t.Fatalf("rollback changed parent ledger: before=%s after=%s", before, after)
				}
				if got := readLegacyIdentities(ctx, t, pg, scope, operation, key); !reflect.DeepEqual(got, map[string]string{original.Operation: original.RequestHash}) {
					t.Fatalf("rollback identities=%v", got)
				}
				replay, err := tryReserve(ctx, t, pg, scope, operation, key, testHash(10))
				if err != nil || !replay.Replay || replay.ResourceID != "workflow-1" {
					t.Fatalf("parent replay=%+v error=%v", replay, err)
				}
			})
		}
	}
}

// parentLedgerRow checks that replay and rollback preserve every ledger column.
func parentLedgerRow(ctx context.Context, t *testing.T, q rowQuerier, scope, operation, key string) string {
	t.Helper()
	query := fmt.Sprintf(`
		SELECT row_to_json(keys)::text FROM %s AS keys
		WHERE scope = $1 AND operation = $2 AND idempotency_key = $3;
	`, postgres.TableCommandIdempotencyKeys)
	var row string
	if err := q.QueryRow(ctx, query, scope, operation, key).Scan(&row); err != nil {
		t.Fatalf("read parent ledger row: %v", err)
	}
	return row
}

// parentLedgerStatus includes the caller's uncommitted writes.
func parentLedgerStatus(ctx context.Context, t *testing.T, q rowQuerier, scope, operation, key string) string {
	t.Helper()
	query := fmt.Sprintf(`
		SELECT status FROM %s
		WHERE scope = $1 AND operation = $2 AND idempotency_key = $3;
	`, postgres.TableCommandIdempotencyKeys)
	var ledgerStatus string
	if err := q.QueryRow(ctx, query, scope, operation, key).Scan(&ledgerStatus); err != nil {
		t.Fatalf("read parent ledger status: %v", err)
	}
	return ledgerStatus
}

// expireKeyInTx makes expiry, replacement, and identity writes roll back together.
func expireKeyInTx(ctx context.Context, t *testing.T, tx pgx.Tx, scope, operation, key string) {
	t.Helper()
	query := fmt.Sprintf(`
		UPDATE %s
		SET expires_at = (now() AT TIME ZONE 'utc') - interval '1 hour'
		WHERE scope = $1 AND operation = $2 AND idempotency_key = $3;
	`, postgres.TableCommandIdempotencyKeys)
	if _, err := tx.Exec(ctx, query, scope, operation, key); err != nil {
		t.Fatalf("expire key in transaction: %v", err)
	}
}

func readLegacyIdentities(ctx context.Context, t *testing.T, pg *postgres.Postgres, scope, operation, key string) map[string]string {
	t.Helper()
	rows, err := pg.Query(ctx, fmt.Sprintf(`
		SELECT legacy_operation, legacy_request_hash FROM %s
		WHERE scope = $1 AND operation = $2 AND idempotency_key = $3;
	`, postgres.TableCommandIdempotencyLegacyIdentities), scope, operation, key)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var legacyOperation, hash string
		require.NoError(t, rows.Scan(&legacyOperation, &hash))
		result[legacyOperation] = hash
	}
	require.NoError(t, rows.Err())
	return result
}
