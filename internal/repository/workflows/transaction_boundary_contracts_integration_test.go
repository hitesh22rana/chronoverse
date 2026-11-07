//nolint:testpackage // Verifies real transaction boundaries through tracing and scoped triggers.
package workflows

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

type (
	terminalLookupCancelKey struct{}
	terminalLookupCancel    struct {
		cancel context.CancelFunc
		fired  atomic.Bool
	}
	terminalLookupProcessor struct{}
)

func (*terminalLookupProcessor) OnStart(ctx context.Context, span sdktrace.ReadWriteSpan) {
	target, ok := ctx.Value(terminalLookupCancelKey{}).(*terminalLookupCancel)
	if !ok || span.Name() != "SELECT" {
		return
	}
	for _, attribute := range span.Attributes() {
		if attribute.Key == "db.query.text" && strings.HasPrefix(strings.TrimSpace(attribute.Value.AsString()), "SELECT EXISTS (SELECT 1 FROM workflows WHERE id = $1 AND user_id = $2)") {
			target.fired.Store(true)
			target.cancel()
			return
		}
	}
}
func (*terminalLookupProcessor) OnEnd(sdktrace.ReadOnlySpan)      {}
func (*terminalLookupProcessor) Shutdown(context.Context) error   { return nil }
func (*terminalLookupProcessor) ForceFlush(context.Context) error { return nil }

// These tests run sequentially; the dedicated pool is closed before provider restoration.
func workflowLookupRepository(t *testing.T) *Repository {
	t.Helper()
	config, err := pgxpool.ParseConfig(testkit.PostgresDSN(t))
	require.NoError(t, err)
	previous := otel.GetTracerProvider()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(&terminalLookupProcessor{}))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		require.NoError(t, provider.Shutdown(ctx))
	})
	pg, err := postgres.New(t.Context(), &postgres.Config{
		Host: config.ConnConfig.Host, Port: int(config.ConnConfig.Port), User: config.ConnConfig.User,
		Password: config.ConnConfig.Password, Database: config.ConnConfig.Database, MaxConns: 1, TLSConfig: &postgres.TLSConfig{},
	})
	require.NoError(t, err)
	t.Cleanup(pg.Close)
	return New(&Config{FetchLimit: 20}, pg)
}

func TestIntegrationLateFailureLookupCancellationRollsBackIdentity(t *testing.T) {
	repo := workflowLookupRepository(t)
	pg := testkit.Postgres(t)
	fixture := seedWorkflowFixture(t.Context(), t, pg, repo, 3)
	_, err := pg.Exec(t.Context(), `UPDATE workflows SET terminated_at=clock_timestamp() AT TIME ZONE 'utc' WHERE id=$1`, fixture.WorkflowID)
	require.NoError(t, err)
	before := readWorkflowMutationState(t.Context(), t, pg, fixture.WorkflowID)
	jobID := uuid.NewString()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	target := &terminalLookupCancel{cancel: cancel}
	ctx = context.WithValue(ctx, terminalLookupCancelKey{}, target)
	reached, err := repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, fixture.WorkflowID, fixture.UserID, jobID)
	require.True(t, target.fired.Load())
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.False(t, reached)
	require.Equal(t, codes.Canceled, status.Code(err))
	require.Contains(t, err.Error(), "context canceled")
	assertMutationState(t, "canceled late failure", readWorkflowMutationState(t.Context(), t, pg, fixture.WorkflowID), before)
	require.Zero(t, countTerminalEffects(t.Context(), t, pg, jobID))
	for range 2 {
		reached, err = repo.IncrementWorkflowConsecutiveJobFailuresCount(t.Context(), fixture.WorkflowID, fixture.UserID, jobID)
		require.NoError(t, err)
		require.False(t, reached)
		require.Equal(t, 1, countTerminalEffects(t.Context(), t, pg, jobID))
		assertMutationState(t, "late failure retry", readWorkflowMutationState(t.Context(), t, pg, fixture.WorkflowID), before)
	}
	require.Zero(t, countWorkflowActionEvents(t.Context(), t, pg, fixture.WorkflowID, "TERMINATE"))
}

func skipWorkflowWrite(t *testing.T, fixture *workflowFixture, predicate string) func() {
	t.Helper()
	pg := testkit.Postgres(t)
	name := "skip_workflow_" + fixtureTag()
	_, err := pg.Exec(t.Context(), fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NULL; END $$`, name))
	require.NoError(t, err)
	remove := func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		_, dropErr := pg.Exec(ctx, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON workflows`, name))
		require.NoError(t, dropErr)
		_, dropErr = pg.Exec(ctx, fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, name))
		require.NoError(t, dropErr)
	}
	t.Cleanup(remove)
	_, err = pg.Exec(t.Context(), fmt.Sprintf(`CREATE TRIGGER %s BEFORE UPDATE ON workflows FOR EACH ROW
 WHEN (NEW.id='%s'::uuid AND (%s)) EXECUTE FUNCTION %s()`, name, fixture.WorkflowID, predicate, name))
	require.NoError(t, err)
	return remove
}

func TestIntegrationBuildTransitionRequiresOneChangedRow(t *testing.T) {
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	fixture := seedWorkflowFixture(t.Context(), t, pg, repo, 3)
	before := readWorkflowMutationState(t.Context(), t, pg, fixture.WorkflowID)
	events := countWorkflowEvents(t.Context(), t, pg, fixture.WorkflowID)
	remove := skipWorkflowWrite(t, fixture, "NEW.build_status='STARTED'")
	err := repo.UpdateWorkflowBuildStatus(t.Context(), fixture.WorkflowID, fixture.UserID, "STARTED", fixture.Generation, "", "")
	require.Equal(t, codes.Internal, status.Code(err))
	require.Contains(t, err.Error(), "workflow build transition invariant violated")
	assertBuildStateUndone(t.Context(), t, pg, fixture, before, events, "skipped transition")
	remove()
	require.NoError(t, repo.UpdateWorkflowBuildStatus(t.Context(), fixture.WorkflowID, fixture.UserID, "STARTED", fixture.Generation, "", ""))
	after := readWorkflowMutationState(t.Context(), t, pg, fixture.WorkflowID)
	expected := *before
	expected.BuildStatus = "STARTED"
	assertMutationState(t, "transition retry", after, &expected)
	require.Equal(t, events, countWorkflowEvents(t.Context(), t, pg, fixture.WorkflowID))
}

func TestIntegrationDatabaseDataFaultDoesNotBlameValidatedIdentity(t *testing.T) {
	for _, operation := range []string{"build", "record failure", "increment failure"} {
		t.Run(operation, func(t *testing.T) {
			pg := testkit.Postgres(t)
			repo := newTestRepository(t)
			fixture := seedWorkflowFixture(t.Context(), t, pg, repo, 3)
			before := readWorkflowMutationState(t.Context(), t, pg, fixture.WorkflowID)
			jobID := uuid.NewString()
			table, event := postgres.TableWorkflows, "UPDATE"
			condition := fmt.Sprintf("NEW.id='%s'::uuid", fixture.WorkflowID)
			message := "failed to update workflow build status"
			switch operation {
			case "record failure":
				table, event = postgres.TableWorkflowTerminalEffects, "INSERT"
				condition = fmt.Sprintf("NEW.job_id='%s'::uuid", jobID)
				message = "failed to record workflow failure event"
			case "increment failure":
				message = "failed to increment consecutive job failures count"
			}
			installFailureEffectFault(t.Context(), t, pg, table, event, condition, "22P02", false, false)
			var err error
			if operation == "build" {
				err = repo.UpdateWorkflowBuildStatus(t.Context(), fixture.WorkflowID, fixture.UserID, "STARTED", fixture.Generation, "", "")
			} else {
				var reached bool
				reached, err = repo.IncrementWorkflowConsecutiveJobFailuresCount(t.Context(), fixture.WorkflowID, fixture.UserID, jobID)
				require.False(t, reached)
			}
			require.Equal(t, codes.Internal, status.Code(err))
			require.Contains(t, err.Error(), message)
			require.Contains(t, err.Error(), "injected failure effect fault")
			assertMutationState(t, "database data fault", readWorkflowMutationState(t.Context(), t, pg, fixture.WorkflowID), before)
			require.Zero(t, countTerminalEffects(t.Context(), t, pg, jobID))
		})
	}
}

func TestIntegrationWorkflowUpdateRequiresOneChangedRow(t *testing.T) {
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	fixture := seedWorkflowFixture(t.Context(), t, pg, repo, 3)
	completeFixtureBuild(t.Context(), t, repo, fixture)
	before := readWorkflowMutationState(t.Context(), t, pg, fixture.WorkflowID)
	events := countWorkflowEvents(t.Context(), t, pg, fixture.WorkflowID)
	command := "metadata-rowcount-" + fixtureTag()
	remove := skipWorkflowWrite(t, fixture, "NEW.name='metadata update'")
	err := repo.UpdateWorkflow(t.Context(), fixture.WorkflowID, fixture.UserID, "metadata update", fixturePayload, fixture.Interval, fixture.MaxConsecutiveJobFailures, command)
	require.Equal(t, codes.Internal, status.Code(err))
	require.Contains(t, err.Error(), "workflow update invariant violated")
	assertMutationState(t, "skipped metadata write", readWorkflowMutationState(t.Context(), t, pg, fixture.WorkflowID), before)
	require.Zero(t, countCommandKeys(t.Context(), t, pg, command))
	require.Equal(t, events, countWorkflowEvents(t.Context(), t, pg, fixture.WorkflowID))
	remove()
	expected := *before
	expected.Name = "metadata update"
	for range 2 {
		require.NoError(t, repo.UpdateWorkflow(t.Context(), fixture.WorkflowID, fixture.UserID, "metadata update", fixturePayload, fixture.Interval, fixture.MaxConsecutiveJobFailures, command))
		assertMutationState(t, "metadata retry/replay", readWorkflowMutationState(t.Context(), t, pg, fixture.WorkflowID), &expected)
		require.Equal(t, 1, countCommandKeys(t.Context(), t, pg, command))
		require.Equal(t, events, countWorkflowEvents(t.Context(), t, pg, fixture.WorkflowID))
	}
}
