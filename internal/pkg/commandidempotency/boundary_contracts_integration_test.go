package commandidempotency_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	ledger "github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

type boundaryLedgerSnapshot struct {
	Hash, Status       string
	Aliases            []string
	Resource, Response sql.NullString
	Created, Updated   time.Time
	Completed, Expires sql.NullTime
}

type ledgerSnapshotReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func readBoundaryLedger(t *testing.T, reader ledgerSnapshotReader, scope string) boundaryLedgerSnapshot {
	t.Helper()
	var state boundaryLedgerSnapshot
	err := reader.QueryRow(t.Context(), `SELECT request_hash,status,request_hash_aliases,resource_id,response::text,
 created_at,updated_at,completed_at,expires_at FROM command_idempotency_keys
 WHERE scope=$1 AND operation=$2 AND idempotency_key=$3`, scope, ledger.OperationUserRegister, "command").Scan(
		&state.Hash, &state.Status, &state.Aliases, &state.Resource, &state.Response, &state.Created, &state.Updated, &state.Completed, &state.Expires,
	)
	require.NoError(t, err)
	return state
}

func cleanupBoundaryTx(t *testing.T, tx pgx.Tx) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Errorf("rollback boundary transaction: %v", err)
		}
	})
}

func TestIntegrationLedgerCompletionValidationKeepsTransactionAndOwner(t *testing.T) {
	pg := testkit.Postgres(t)
	scope := "completion-boundary-" + uuid.NewString()
	key := "command"
	hash := testHash(71)
	tx := reserveTx(t.Context(), t, pg)
	cleanupBoundaryTx(t, tx)
	reservation, err := ledger.Reserve(t.Context(), tx, scope, ledger.OperationUserRegister, key, hash)
	require.NoError(t, err)
	require.False(t, reservation.Replay)
	before := readBoundaryLedger(t, tx, scope)
	err = ledger.Complete(t.Context(), tx, scope, ledger.OperationUserRegister, "bad\nkey", hash, "resource", map[string]string{"id": "resource"}, time.Hour)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, before, readBoundaryLedger(t, tx, scope))
	err = ledger.Complete(t.Context(), tx, scope, ledger.OperationUserRegister, key, hash, "resource", math.Inf(1), time.Hour)
	require.Equal(t, codes.Internal, status.Code(err))
	require.Contains(t, err.Error(), "failed to encode idempotency response")
	require.Equal(t, before, readBoundaryLedger(t, tx, scope))
	require.NoError(t, ledger.Complete(t.Context(), tx, scope, ledger.OperationUserRegister, key, hash, "resource", map[string]string{"id": "resource"}, time.Hour))
	require.NoError(t, tx.Commit(t.Context()))
	after := readBoundaryLedger(t, pg, scope)
	require.Equal(t, "COMPLETED", after.Status)
	require.Equal(t, hash, after.Hash)
	require.Equal(t, "resource", after.Resource.String)
	require.True(t, after.Completed.Valid)
	require.True(t, after.Expires.Valid)
	require.Equal(t, time.Hour, after.Expires.Time.Sub(after.Completed.Time))
	require.JSONEq(t, `{"id":"resource"}`, after.Response.String)
}

func TestIntegrationLedgerReplaysSharedAliasWithoutRenewingOwnership(t *testing.T) {
	pg := testkit.Postgres(t)
	scope := "alias-boundary-" + uuid.NewString()
	key := "command"
	oldHash, newHash, commonHash := testHash(81), testHash(82), testHash(83)
	tx := reserveTx(t.Context(), t, pg)
	cleanupBoundaryTx(t, tx)
	reservation, err := ledger.Reserve(t.Context(), tx, scope, ledger.OperationUserRegister, key, oldHash, commonHash)
	require.NoError(t, err)
	require.False(t, reservation.Replay)
	require.NoError(t, ledger.Complete(t.Context(), tx, scope, ledger.OperationUserRegister, key, oldHash, "resource", map[string]string{"id": "resource"}, time.Hour))
	require.NoError(t, tx.Commit(t.Context()))
	before := readBoundaryLedger(t, pg, scope)
	replayTx := reserveTx(t.Context(), t, pg)
	cleanupBoundaryTx(t, replayTx)
	replay, err := ledger.Reserve(t.Context(), replayTx, scope, ledger.OperationUserRegister, key, newHash, commonHash)
	require.NoError(t, err)
	require.True(t, replay.Replay)
	require.Equal(t, "resource", replay.ResourceID)
	require.JSONEq(t, `{"id":"resource"}`, string(replay.Response))
	require.NoError(t, replayTx.Commit(t.Context()))
	require.Equal(t, before, readBoundaryLedger(t, pg, scope))
}

type (
	reserveReadCancelKey struct{}
	reserveReadCancel    struct {
		cancel context.CancelFunc
		fired  atomic.Bool
	}
	reserveReadProcessor struct{}
)

func (*reserveReadProcessor) OnStart(ctx context.Context, span sdktrace.ReadWriteSpan) {
	target, ok := ctx.Value(reserveReadCancelKey{}).(*reserveReadCancel)
	if !ok || span.Name() != "SELECT" {
		return
	}
	for _, attribute := range span.Attributes() {
		if attribute.Key == "db.query.text" && strings.HasPrefix(strings.TrimSpace(attribute.Value.AsString()), "SELECT request_hash, request_hash_aliases, status, resource_id, response") {
			target.fired.Store(true)
			target.cancel()
			return
		}
	}
}
func (*reserveReadProcessor) OnEnd(sdktrace.ReadOnlySpan)      {}
func (*reserveReadProcessor) Shutdown(context.Context) error   { return nil }
func (*reserveReadProcessor) ForceFlush(context.Context) error { return nil }

// The dedicated pool closes before the temporary provider is restored; tests are sequential.
func reserveReadPool(t *testing.T) (*postgres.Postgres, *sdktrace.TracerProvider) {
	t.Helper()
	config, err := pgxpool.ParseConfig(testkit.PostgresDSN(t))
	require.NoError(t, err)
	previous := otel.GetTracerProvider()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(&reserveReadProcessor{}))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
		defer cancel()
		require.NoError(t, provider.Shutdown(ctx))
	})
	pool, err := postgres.New(t.Context(), &postgres.Config{
		Host: config.ConnConfig.Host, Port: int(config.ConnConfig.Port), User: config.ConnConfig.User,
		Password: config.ConnConfig.Password, Database: config.ConnConfig.Database, MaxConns: 1, TLSConfig: &postgres.TLSConfig{},
	})
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool, provider
}

func TestIntegrationLedgerReplayReadFailurePreservesCompletedCommand(t *testing.T) {
	pg, provider := reserveReadPool(t)
	scope := "read-boundary-" + uuid.NewString()
	key := "command"
	hash := testHash(91)
	initialTx := reserveTx(t.Context(), t, pg)
	cleanupBoundaryTx(t, initialTx)
	initial, err := ledger.Reserve(t.Context(), initialTx, scope, ledger.OperationUserRegister, key, hash)
	require.NoError(t, err)
	require.False(t, initial.Replay)
	require.NoError(t, ledger.Complete(t.Context(), initialTx, scope, ledger.OperationUserRegister, key, hash, "resource", json.RawMessage(`{"id":"resource"}`), time.Hour))
	require.NoError(t, initialTx.Commit(t.Context()))
	before := readBoundaryLedger(t, pg, scope)
	replayTx := reserveTx(t.Context(), t, pg)
	cleanupBoundaryTx(t, replayTx)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	target := &reserveReadCancel{cancel: cancel}
	ctx = context.WithValue(ctx, reserveReadCancelKey{}, target)
	ctx, span := provider.Tracer("reserve-read-contract").Start(ctx, "ReserveReplay")
	defer span.End()
	result, err := ledger.Reserve(ctx, replayTx, scope, ledger.OperationUserRegister, key, hash)
	require.True(t, target.fired.Load())
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.Nil(t, result)
	require.Equal(t, codes.Internal, status.Code(err))
	require.Contains(t, err.Error(), "failed to read command idempotency key")
	require.Contains(t, err.Error(), "context canceled")
	cleanup, cancelCleanup := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
	defer cancelCleanup()
	require.NoError(t, replayTx.Rollback(cleanup))
	require.Equal(t, before, readBoundaryLedger(t, pg, scope))
	retryTx := reserveTx(t.Context(), t, pg)
	cleanupBoundaryTx(t, retryTx)
	retry, err := ledger.Reserve(t.Context(), retryTx, scope, ledger.OperationUserRegister, key, hash)
	require.NoError(t, err)
	require.True(t, retry.Replay)
	require.Equal(t, "resource", retry.ResourceID)
	require.JSONEq(t, `{"id":"resource"}`, string(retry.Response))
	require.NoError(t, retryTx.Commit(t.Context()))
	require.Equal(t, before, readBoundaryLedger(t, pg, scope))
}
