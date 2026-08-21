// Package commandidempotency_test exercises the durable command ledger against
// a real PostgreSQL instance provisioned by Testcontainers.
package commandidempotency_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestMain(m *testing.M) {
	testkit.Run(m, testkit.WithPostgres())
}

func TestIntegrationLedgerReserveAndCompleteLifecycle(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	scope := "ledger-it-" + t.Name()
	const operation = commandidempotency.OperationUserRegister
	key, requestHash := "lifecycle-key", testHash(1)

	// A fresh reservation is PROCESSING with no recorded outcome.
	fresh, freshErr := tryReserve(ctx, t, pg, scope, operation, key, requestHash)
	if freshErr != nil {
		t.Fatalf("Reserve (fresh): %v", freshErr)
	}
	if fresh.Replay || fresh.ResourceID != "" || fresh.Response != nil {
		t.Fatalf("fresh reservation = %+v, want an empty PROCESSING reservation", fresh)
	}

	// A second reserve while the command is still processing aborts.
	_, inFlightErr := tryReserve(ctx, t, pg, scope, operation, key, requestHash)
	if status.Code(inFlightErr) != codes.Aborted {
		t.Fatalf("Reserve (in-flight) code = %v, want %v (err: %v)", status.Code(inFlightErr), codes.Aborted, inFlightErr)
	}

	// Reusing the key with a different request conflicts while processing.
	_, conflictErr := tryReserve(ctx, t, pg, scope, operation, key, testHash(2))
	if status.Code(conflictErr) != codes.AlreadyExists {
		t.Fatalf("Reserve (hash conflict) code = %v, want %v (err: %v)", status.Code(conflictErr), codes.AlreadyExists, conflictErr)
	}

	// Completion is compare-and-set on the owning request hash.
	mismatched := tryComplete(ctx, t, pg, scope, operation, key, testHash(2), "resource-1", map[string]string{"id": "resource-1"}, commandidempotency.ClientCommandRetention)
	if status.Code(mismatched) != codes.Internal {
		t.Fatalf("Complete (mismatched hash) code = %v, want %v (err: %v)", status.Code(mismatched), codes.Internal, mismatched)
	}
	if completeErr := tryComplete(ctx, t, pg, scope, operation, key, requestHash, "resource-1", map[string]string{"id": "resource-1"}, commandidempotency.ClientCommandRetention); completeErr != nil {
		t.Fatalf("Complete: %v", completeErr)
	}
	// Completing twice violates the single PROCESSING invariant.
	doubleErr := tryComplete(ctx, t, pg, scope, operation, key, requestHash, "resource-1", map[string]string{"id": "resource-1"}, commandidempotency.ClientCommandRetention)
	if status.Code(doubleErr) != codes.Internal {
		t.Fatalf("Complete (already completed) code = %v, want %v (err: %v)", status.Code(doubleErr), codes.Internal, doubleErr)
	}
}

func TestIntegrationLedgerReplaysCompletedCommand(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	scope := "ledger-it-" + t.Name()
	const operation = commandidempotency.OperationUserRegister
	key, requestHash := "replay-key", testHash(5)

	if _, err := tryReserve(ctx, t, pg, scope, operation, key, requestHash); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if completeErr := tryComplete(ctx, t, pg, scope, operation, key, requestHash, "resource-1", map[string]string{"id": "resource-1"}, commandidempotency.ClientCommandRetention); completeErr != nil {
		t.Fatalf("Complete: %v", completeErr)
	}

	// The completed command replays with its stored resource and response.
	replay, replayErr := tryReserve(ctx, t, pg, scope, operation, key, requestHash)
	if replayErr != nil {
		t.Fatalf("Reserve (replay): %v", replayErr)
	}
	if !replay.Replay || replay.ResourceID != "resource-1" {
		t.Fatalf("replay reservation = %+v, want replay of resource-1", replay)
	}
	var response map[string]string
	if err := json.Unmarshal(replay.Response, &response); err != nil {
		t.Fatalf("unmarshal replay response %s: %v", replay.Response, err)
	}
	if response["id"] != "resource-1" {
		t.Fatalf("replay response = %+v, want id resource-1", response)
	}

	// Conflicts persist after completion.
	_, postConflictErr := tryReserve(ctx, t, pg, scope, operation, key, testHash(3))
	if status.Code(postConflictErr) != codes.AlreadyExists {
		t.Fatalf("Reserve (post-completion conflict) code = %v, want %v (err: %v)", status.Code(postConflictErr), codes.AlreadyExists, postConflictErr)
	}
}

func TestIntegrationLedgerAcceptsCompatibleLegacyHashes(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	scope := "ledger-it-" + t.Name()
	const operation = commandidempotency.OperationWorkflowCreate
	key, requestHash, legacyHash := "compat-key", testHash(10), testHash(11)

	fresh, freshErr := tryReserve(ctx, t, pg, scope, operation, key, requestHash, legacyHash)
	if freshErr != nil {
		t.Fatalf("Reserve (with legacy alias): %v", freshErr)
	}
	if fresh.Replay {
		t.Fatalf("reservation = %+v, want a fresh reservation", fresh)
	}
	if completeErr := tryComplete(ctx, t, pg, scope, operation, key, requestHash, "workflow-1", nil, commandidempotency.ClientCommandRetention); completeErr != nil {
		t.Fatalf("Complete: %v", completeErr)
	}

	// The stored alias identifies the same command, so the pre-migration
	// spelling replays instead of conflicting.
	replay, replayErr := tryReserve(ctx, t, pg, scope, operation, key, legacyHash)
	if replayErr != nil {
		t.Fatalf("Reserve (legacy hash): %v", replayErr)
	}
	if !replay.Replay || replay.ResourceID != "workflow-1" {
		t.Fatalf("legacy replay = %+v, want replay of workflow-1", replay)
	}
}

func TestIntegrationLedgerExpiredKeyIsReplacedAsFresh(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	scope := "ledger-it-" + t.Name()
	const operation = commandidempotency.OperationNotificationCreate
	key, requestHash := "expiring-key", testHash(20)

	if _, err := tryReserve(ctx, t, pg, scope, operation, key, requestHash); err != nil {
		t.Fatalf("Reserve (first): %v", err)
	}
	if completeErr := tryComplete(ctx, t, pg, scope, operation, key, requestHash, "notification-1", map[string]string{"id": "notification-1"}, time.Minute); completeErr != nil {
		t.Fatalf("Complete: %v", completeErr)
	}
	expireKey(ctx, t, pg, scope, operation, key)

	// An expired completed record no longer replays: the key can be reserved
	// again as a fresh PROCESSING command.
	replacement, replacementErr := tryReserve(ctx, t, pg, scope, operation, key, requestHash)
	if replacementErr != nil {
		t.Fatalf("Reserve (after expiry): %v", replacementErr)
	}
	if replacement.Replay || replacement.ResourceID != "" {
		t.Fatalf("post-expiry reservation = %+v, want a fresh reservation", replacement)
	}

	// Until it completes again, the replaced reservation is still processing.
	if _, inFlightErr := tryReserve(ctx, t, pg, scope, operation, key, requestHash); status.Code(inFlightErr) != codes.Aborted {
		t.Fatalf("Reserve (replacement in-flight) code = %v, want %v (err: %v)", status.Code(inFlightErr), codes.Aborted, inFlightErr)
	}
}

func TestIntegrationLedgerRejectsInvalidKeys(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	scope := "ledger-it-" + t.Name()
	const operation = commandidempotency.OperationJobCancel

	for name, key := range map[string]string{
		"empty":        "",
		"blank":        "   ",
		"too long":     string(make([]byte, 256)),
		"control char": "key\x07",
	} {
		if _, err := tryReserve(ctx, t, pg, scope, operation, key, testHash(30)); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("Reserve (%s key) code = %v, want %v (err: %v)", name, status.Code(err), codes.InvalidArgument, err)
		}
	}
}

// testHash returns a valid 64-character hexadecimal request hash.
func testHash(n int) string {
	return fmt.Sprintf("%064x", n)
}

// reserveTx begins a transaction for one ledger operation.
func reserveTx(ctx context.Context, t *testing.T, pg *postgres.Postgres) pgx.Tx {
	t.Helper()
	tx, err := pg.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	return tx
}

// tryReserve reserves a command identity in its own transaction, committing on
// success and rolling back on ledger errors.
func tryReserve(
	ctx context.Context,
	t *testing.T,
	pg *postgres.Postgres,
	scope, operation, key, requestHash string,
	compatibleRequestHashes ...string,
) (*commandidempotency.Reservation, error) {
	t.Helper()

	tx := reserveTx(ctx, t, pg)
	reservation, err := commandidempotency.Reserve(ctx, tx, scope, operation, key, requestHash, compatibleRequestHashes...)
	if err != nil {
		//nolint:errcheck // Best-effort rollback before returning the error.
		_ = tx.Rollback(ctx)
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit reservation: %v", err)
	}
	return reservation, nil
}

// tryComplete completes a command identity in its own transaction.
func tryComplete(
	ctx context.Context,
	t *testing.T,
	pg *postgres.Postgres,
	scope, operation, key, requestHash, resourceID string,
	response any,
	retention time.Duration,
) error {
	t.Helper()

	tx := reserveTx(ctx, t, pg)
	if err := commandidempotency.Complete(ctx, tx, scope, operation, key, requestHash, resourceID, response, retention); err != nil {
		//nolint:errcheck // Best-effort rollback before returning the error.
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

// expireKey moves a completed record's expiry into the past so the next
// reservation replaces it, mimicking elapsed retention.
func expireKey(ctx context.Context, t *testing.T, pg *postgres.Postgres, scope, operation, key string) {
	t.Helper()
	if _, err := pg.Exec(ctx, fmt.Sprintf(`
		UPDATE %s
		SET expires_at = (now() AT TIME ZONE 'utc') - interval '1 hour'
		WHERE scope = $1 AND operation = $2 AND idempotency_key = $3
	`, postgres.TableCommandIdempotencyKeys), scope, operation, key); err != nil {
		t.Fatalf("expire key: %v", err)
	}
}
