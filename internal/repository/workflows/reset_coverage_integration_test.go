//nolint:testpackage // Integration tests reuse repository fixtures and scoped database faults.
package workflows

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestIntegrationResetFaultsRollBack(t *testing.T) {
	cases := []struct {
		name, table, event, message string
		deferred                    bool
	}{
		{name: "record completion", table: postgres.TableWorkflowTerminalEffects, event: "INSERT", message: "failed to reset consecutive job failures count"},
		{name: "reset counter", table: postgres.TableWorkflows, event: "UPDATE", message: "failed to reset workflow failures"},
		{name: "commit completion", table: postgres.TableWorkflowTerminalEffects, event: "INSERT", deferred: true, message: "failed to commit completed terminal-effect reset"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			pg := testkit.Postgres(t)
			repo := newTestRepository(t)
			fixture := seedWorkflowFixture(ctx, t, pg, repo, 4)
			if _, err := repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, fixture.WorkflowID, fixture.UserID, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			before := readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID)
			if before.ConsecutiveJobFailuresCount != 1 {
				t.Fatalf("fixture failures=%d, want 1", before.ConsecutiveJobFailuresCount)
			}
			jobID := uuid.NewString()
			condition := fmt.Sprintf("NEW.workflow_id = '%s'::uuid", fixture.WorkflowID)
			if tc.table == postgres.TableWorkflows {
				condition = fmt.Sprintf("NEW.id = '%s'::uuid", fixture.WorkflowID)
			}
			installFailureEffectFault(ctx, t, pg, tc.table, tc.event, condition, tc.deferred, false)
			err := repo.ResetWorkflowConsecutiveJobFailuresCount(ctx, fixture.WorkflowID, fixture.UserID, jobID)
			if status.Code(err) != codes.Internal || !strings.Contains(status.Convert(err).Message(), "injected failure effect fault") || !strings.Contains(status.Convert(err).Message(), tc.message) {
				t.Fatalf("reset = %v, want Internal containing %q and %q", err, "injected failure effect fault", tc.message)
			}
			assertFailureState(t, "failed reset", readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID), before)
			if got := countTerminalEffects(ctx, t, pg, jobID); got != 0 {
				t.Fatalf("completion effects=%d, want 0", got)
			}
		})
	}
}

func TestIntegrationResetReplayPreservesLaterFailures(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	fixture := seedWorkflowFixture(ctx, t, pg, repo, 4)
	completionID := uuid.NewString()
	if _, err := repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, fixture.WorkflowID, fixture.UserID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err := repo.ResetWorkflowConsecutiveJobFailuresCount(ctx, fixture.WorkflowID, fixture.UserID, completionID); err != nil {
		t.Fatal(err)
	}
	state := readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID)
	if state.ConsecutiveJobFailuresCount != 0 {
		t.Fatalf("count=%d, want 0", state.ConsecutiveJobFailuresCount)
	}
	if _, err := repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, fixture.WorkflowID, fixture.UserID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	before := readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID)
	if before.ConsecutiveJobFailuresCount != 1 {
		t.Fatalf("fixture failures=%d, want 1", before.ConsecutiveJobFailuresCount)
	}
	if err := repo.ResetWorkflowConsecutiveJobFailuresCount(ctx, strings.ToUpper(fixture.WorkflowID), strings.ToUpper(fixture.UserID), strings.ToUpper(completionID)); err != nil {
		t.Fatal(err)
	}
	assertFailureState(t, "replayed completion after new failure", readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID), before)
	if before.ConsecutiveJobFailuresCount != 1 || countTerminalEffects(ctx, t, pg, completionID) != 1 {
		t.Fatal("replay changed later failure or duplicated completion identity")
	}
	if err := repo.ResetWorkflowConsecutiveJobFailuresCount(ctx, uuid.NewString(), fixture.UserID, uuid.NewString()); status.Code(err) != codes.NotFound {
		t.Fatalf("missing workflow error=%v", err)
	}
}

func TestIntegrationResetInterruptedInsertLeavesNoEffects(t *testing.T) {
	for _, stop := range []string{"cancel", "deadline", "server"} {
		t.Run(stop, func(t *testing.T) {
			ctx := context.Background()
			pg := testkit.Postgres(t)
			repo := newTestRepository(t)
			fixture := seedWorkflowFixture(ctx, t, pg, repo, 4)
			if _, err := repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, fixture.WorkflowID, fixture.UserID, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			before := readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID)
			if before.ConsecutiveJobFailuresCount != 1 {
				t.Fatalf("fixture failures=%d, want 1", before.ConsecutiveJobFailuresCount)
			}
			jobID := uuid.NewString()
			lock := interruptionLock{
				label:   "completion effect insert",
				lockSQL: "LOCK TABLE " + postgres.TableWorkflowTerminalEffects + " IN SHARE ROW EXCLUSIVE MODE",
				pattern: "%INSERT INTO " + postgres.TableWorkflowTerminalEffects + "%",
			}
			callerCtx, step := callerStopPlan(ctx, t, pg, stop, lock)
			err := runWhileBlocked(callerCtx, t, pg, lock, step, func(commandCtx context.Context) error {
				return repo.ResetWorkflowConsecutiveJobFailuresCount(commandCtx, fixture.WorkflowID, fixture.UserID, jobID)
			})
			want := codes.Internal
			if stop == "cancel" {
				want = codes.Canceled
			}
			if stop == "deadline" {
				want = codes.DeadlineExceeded
			}
			if status.Code(err) != want {
				t.Fatalf("error=%v, want %v", err, want)
			}
			assertFailureState(t, "interrupted reset", readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID), before)
			if countTerminalEffects(ctx, t, pg, jobID) != 0 {
				t.Fatal("interrupted reset persisted completion")
			}
			if err := repo.ResetWorkflowConsecutiveJobFailuresCount(ctx, fixture.WorkflowID, fixture.UserID, jobID); err != nil {
				t.Fatalf("retry: %v", err)
			}
			if state := readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID); state.ConsecutiveJobFailuresCount != 0 || countTerminalEffects(ctx, t, pg, jobID) != 1 {
				t.Fatal("retry did not atomically reset counter and record completion")
			}
		})
	}
}

func TestIntegrationResetCanceledTransactionStart(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	fixture := seedWorkflowFixture(ctx, t, pg, repo, 4)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	err := repo.ResetWorkflowConsecutiveJobFailuresCount(canceled, fixture.WorkflowID, fixture.UserID, uuid.NewString())
	if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "failed to start completed terminal-effect transaction") {
		t.Fatalf("error=%v, want failed transaction start", err)
	}
}
