//nolint:testpackage // Exercises real pgx COMMIT cancellation through tracing hooks.
package jobs

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

type commitCancelContextKey struct{}

type queryCancelContextKey struct{}

type queryCancelTarget struct {
	operation string
	sqlPrefix string
	cancel    context.CancelFunc
	fired     atomic.Bool
}

// OnStart runs before pgx sends the marked statement. Only private context
// markers cancel callers; other queries retain their original context.
type commitCancelProcessor struct{}

func (*commitCancelProcessor) OnStart(ctx context.Context, span sdktrace.ReadWriteSpan) {
	if cancel, ok := ctx.Value(commitCancelContextKey{}).(context.CancelFunc); ok && span.Name() == "COMMIT" {
		cancel()
	}
	target, ok := ctx.Value(queryCancelContextKey{}).(*queryCancelTarget)
	if !ok || span.Name() != target.operation {
		return
	}
	for _, attribute := range span.Attributes() {
		if attribute.Key == "db.query.text" && strings.HasPrefix(strings.TrimSpace(attribute.Value.AsString()), target.sqlPrefix) {
			target.fired.Store(true)
			target.cancel()
			return
		}
	}
}
func (*commitCancelProcessor) OnEnd(sdktrace.ReadOnlySpan)      {}
func (*commitCancelProcessor) Shutdown(context.Context) error   { return nil }
func (*commitCancelProcessor) ForceFlush(context.Context) error { return nil }

// These tests run sequentially. A dedicated pool captures the temporary provider;
// it is closed before the original global provider is restored.
func newCommitCancellationRepository(t *testing.T) *Repository {
	t.Helper()
	dsn := testkit.PostgresDSN(t)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	previous := otel.GetTracerProvider()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(&commitCancelProcessor{}))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if shutdownErr := provider.Shutdown(shutdownCtx); shutdownErr != nil {
			t.Errorf("shutdown commit tracer: %v", shutdownErr)
		}
	})
	pg, err := postgres.New(t.Context(), &postgres.Config{
		Host: cfg.ConnConfig.Host, Port: int(cfg.ConnConfig.Port), User: cfg.ConnConfig.User, Password: cfg.ConnConfig.Password, Database: cfg.ConnConfig.Database,
		MaxConns: 1, TLSConfig: &postgres.TLSConfig{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pg.Close)
	repo := newTestRepository(t)
	repo.pg = pg
	return repo
}

func cancelAtCommitContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	return context.WithValue(ctx, commitCancelContextKey{}, cancel)
}

func TestIntegrationTerminalReplayCommitCancellationPreservesEffects(t *testing.T) {
	repo := newCommitCancellationRepository(t)
	pg := testkit.Postgres(t)
	for _, operation := range terminalCommandOperations {
		t.Run(operation.Name, func(t *testing.T) {
			fixture := seedClaimedJob(t.Context(), t, pg, repo)
			command := "terminal-commit-context-" + fixtureTag()
			if err := operation.Invoke(t.Context(), repo, fixture.JobID, fixture.LeaseToken, command); err != nil {
				t.Fatal(err)
			}
			before := readTerminalJobState(t.Context(), t, pg, fixture.JobID)
			ledgerBefore, exists := readJobCommand(t.Context(), t, pg, fixture.JobID, operation.Operation, command)
			if !exists {
				t.Fatal("initial command missing")
			}
			err := operation.Invoke(cancelAtCommitContext(t), repo, fixture.JobID, fixture.LeaseToken, command)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("replay commit error=%v, want context cancellation", err)
			}
			if after := readTerminalJobState(t.Context(), t, pg, fixture.JobID); !reflect.DeepEqual(after, before) {
				t.Fatalf("failed replay changed job: %+v -> %+v", before, after)
			}
			ledgerAfter, exists := readJobCommand(t.Context(), t, pg, fixture.JobID, operation.Operation, command)
			if !exists {
				t.Fatal("failed replay lost command")
			}
			assertSameLedgerRow(t, ledgerAfter, ledgerBefore)
			assertOutboxEffectCount(t.Context(), t, pg, fixture, operation.Operation)
			if err := operation.Invoke(t.Context(), repo, fixture.JobID, fixture.LeaseToken, command); err != nil {
				t.Fatalf("retry replay: %v", err)
			}
			assertOutboxEffectCount(t.Context(), t, pg, fixture, operation.Operation)
		})
	}
}

func TestIntegrationScheduleReplayCommitCancellationPreservesIdentity(t *testing.T) {
	repo := newCommitCancellationRepository(t)
	pg := testkit.Postgres(t)
	user, workflow := seedIsolatedWorkflow(t.Context(), t, pg)
	command := "schedule-commit-context-" + fixtureTag()
	instant := time.Now().UTC().Format(time.RFC3339Nano)
	first, err := repo.ScheduleJob(t.Context(), workflow, user, instant, "MANUAL", command, 1)
	if err != nil {
		t.Fatal(err)
	}
	before := readTerminalJobState(t.Context(), t, pg, first)
	ledgerBefore, exists := readCommandByScope(t.Context(), t, pg, commandidempotency.UserScope(user), commandidempotency.ManualScheduleOperation(workflow), command)
	if !exists {
		t.Fatal("initial schedule command missing")
	}
	replay, err := repo.ScheduleJob(cancelAtCommitContext(t), workflow, user, instant, "MANUAL", command, 1)
	if replay != "" || status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "failed to commit schedule replay") {
		t.Fatalf("replay=%q error=%v", replay, err)
	}
	if after := readTerminalJobState(t.Context(), t, pg, first); !reflect.DeepEqual(after, before) {
		t.Fatalf("failed replay changed job: %+v -> %+v", before, after)
	}
	ledgerAfter, exists := readCommandByScope(t.Context(), t, pg, commandidempotency.UserScope(user), commandidempotency.ManualScheduleOperation(workflow), command)
	if !exists {
		t.Fatal("failed replay lost schedule command")
	}
	assertSameLedgerRow(t, ledgerAfter, ledgerBefore)
	retry, err := repo.ScheduleJob(t.Context(), workflow, user, instant, "MANUAL", command, 1)
	if err != nil || retry != first {
		t.Fatalf("retry=%q error=%v, want %q", retry, err, first)
	}
	var count int
	if err := pg.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE workflow_id=$1`, workflow).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("jobs=%d, want exactly one", count)
	}
}

func TestIntegrationCancelReplayCommitCancellationPreservesSnapshot(t *testing.T) {
	repo := newCommitCancellationRepository(t)
	pg := testkit.Postgres(t)
	fixture := seedClaimedJob(t.Context(), t, pg, repo)
	command := "cancel-commit-context-" + fixtureTag()
	first, err := repo.CancelJob(t.Context(), fixture.JobID, command, "OPERATOR_REQUEST")
	if err != nil {
		t.Fatal(err)
	}
	before := readTerminalJobState(t.Context(), t, pg, fixture.JobID)
	ledgerBefore, exists := readJobCommand(t.Context(), t, pg, fixture.JobID, commandidempotency.OperationJobCancel, command)
	if !exists {
		t.Fatal("initial cancel command missing")
	}
	replay, err := repo.CancelJob(cancelAtCommitContext(t), fixture.JobID, command, "OPERATOR_REQUEST")
	if replay != nil || status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "failed to commit cancel replay") {
		t.Fatalf("replay=%+v error=%v", replay, err)
	}
	if after := readTerminalJobState(t.Context(), t, pg, fixture.JobID); !reflect.DeepEqual(after, before) {
		t.Fatalf("failed replay changed job: %+v -> %+v", before, after)
	}
	ledgerAfter, exists := readJobCommand(t.Context(), t, pg, fixture.JobID, commandidempotency.OperationJobCancel, command)
	if !exists {
		t.Fatal("failed replay lost cancel command")
	}
	assertSameLedgerRow(t, ledgerAfter, ledgerBefore)
	retry, err := repo.CancelJob(t.Context(), fixture.JobID, command, "OPERATOR_REQUEST")
	if err != nil || !reflect.DeepEqual(retry, first) {
		t.Fatalf("retry=%+v error=%v, want %+v", retry, err, first)
	}
	assertNoOutboxEffect(t.Context(), t, pg, fixture)
}

// Operation and SQL are both required: command reservation and recovery each
// begin WITH, so matching only an operation would cancel before the target phase.
func cancelAtQueryContext(t *testing.T, operation, sqlPrefix string) (context.Context, *queryCancelTarget) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	target := &queryCancelTarget{operation: operation, sqlPrefix: strings.TrimSpace(sqlPrefix), cancel: cancel}
	return context.WithValue(ctx, queryCancelContextKey{}, target), target
}
