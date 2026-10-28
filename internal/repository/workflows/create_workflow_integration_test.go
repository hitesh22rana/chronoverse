//nolint:testpackage // Reuses repository fixtures and scoped PostgreSQL faults.
package workflows

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

func assertCreateEffects(ctx context.Context, t *testing.T, repo *Repository, userID string, wantWorkflows, wantEvents, wantCommands, wantAliases int) {
	t.Helper()
	var workflows, events, commands, aliases int
	err := repo.pg.QueryRow(ctx, `SELECT
  (SELECT count(*) FROM workflows WHERE user_id=$1::uuid),
  (SELECT count(*) FROM outbox_events WHERE payload->>'UserID'=$1::text),
  (SELECT count(*) FROM command_idempotency_keys WHERE scope=$2),
  (SELECT count(*) FROM command_idempotency_legacy_identities WHERE scope=$2)`, userID, commandidempotency.UserScope(userID)).Scan(&workflows, &events, &commands, &aliases)
	if err != nil {
		t.Fatal(err)
	}
	if workflows != wantWorkflows || events != wantEvents || commands != wantCommands || aliases != wantAliases {
		t.Fatalf("effects workflows/events/commands/aliases=%d/%d/%d/%d, want %d/%d/%d/%d", workflows, events, commands, aliases, wantWorkflows, wantEvents, wantCommands, wantAliases)
	}
}

func assertBuildEvent(ctx context.Context, t *testing.T, repo *Repository, workflowID string, generation int64) {
	t.Helper()
	build := workflowsmodel.ActionBuild.ToString()
	assertEventKeys(
		t,
		"build events",
		workflowActionEventKeys(ctx, t, repo.pg, workflowID, build),
		idempotency.WorkflowEventKey(workflowID, build, generation),
	)
}

func TestIntegrationCreateWorkflowFaultsRollBack(t *testing.T) {
	for _, tc := range []struct {
		name, table, event, message string
		deferred                    bool
	}{
		{"reserve", postgres.TableCommandIdempotencyKeys, "INSERT", "failed to reserve command idempotency key", false},
		{"legacy identity", postgres.TableCommandIdempotencyLegacyIdentities, "INSERT", "failed to store command legacy identity", false},
		{"workflow", postgres.TableWorkflows, "INSERT", "failed to insert workflow", false},
		{"outbox", postgres.TableOutboxEvents, "INSERT", "failed to insert outbox event", false},
		{"complete", postgres.TableCommandIdempotencyKeys, "UPDATE", "failed to complete command idempotency key", false},
		{"commit", postgres.TableWorkflows, "INSERT", "failed to commit transaction", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			repo := newTestRepository(t)
			userID := seedUser(ctx, t, repo.pg)
			condition := fmt.Sprintf("NEW.scope = '%s'", commandidempotency.UserScope(userID))
			if tc.table == postgres.TableWorkflows {
				condition = fmt.Sprintf("NEW.user_id = '%s'::uuid", userID)
			}
			if tc.table == postgres.TableOutboxEvents {
				condition = fmt.Sprintf("NEW.payload->>'UserID' = '%s'", userID)
			}
			installFailureEffectFault(ctx, t, repo.pg, tc.table, tc.event, condition, "", tc.deferred, false)
			got, err := repo.CreateWorkflow(ctx, userID, "create-fault", `{"image":"alpine:3.22.2"}`, "CONTAINER", 60, 3, true, uuid.NewString())
			if got != nil || status.Code(err) != codes.Internal ||
				!strings.Contains(status.Convert(err).Message(), "injected failure effect fault") ||
				!strings.Contains(status.Convert(err).Message(), tc.message) {
				t.Fatalf("CreateWorkflow=%+v err=%v, want Internal containing %q", got, err, tc.message)
			}
			assertCreateEffects(ctx, t, repo, userID, 0, 0, 0, 0)
		})
	}
}

func TestIntegrationCreateWorkflowRejectedRequestsLeaveNoEffects(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository(t)
	userID := seedUser(ctx, t, repo.pg)
	for _, tc := range []struct{ name, payload, kind, key string }{
		{"invalid JSON", "{", "CONTAINER", uuid.NewString()},
		{"invalid container", `{}`, "CONTAINER", uuid.NewString()},
		{"unknown kind", `{}`, "INVALID", uuid.NewString()},
		{"empty key", `{"image":"alpine:3.22.2"}`, "CONTAINER", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repo.CreateWorkflow(ctx, userID, "create-invalid", tc.payload, tc.kind, 60, 3, true, tc.key)
			if got != nil || status.Code(err) != codes.InvalidArgument {
				t.Fatalf("CreateWorkflow=%+v err=%v, want InvalidArgument", got, err)
			}
			assertCreateEffects(ctx, t, repo, userID, 0, 0, 0, 0)
		})
	}
	got, err := repo.CreateWorkflow(ctx, "invalid", "bad-user", `{"endpoint":"https://example.com","timeout":"10s"}`, "HEARTBEAT", 60, 3, true, uuid.NewString())
	if got != nil || status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid user result=%+v err=%v", got, err)
	}
	for _, expired := range []bool{false, true} {
		var caller context.Context
		var cancel context.CancelFunc
		code := codes.Canceled
		if expired {
			caller, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
			code = codes.DeadlineExceeded
		} else {
			caller, cancel = context.WithCancel(ctx)
		}
		cancel()
		got, err = repo.CreateWorkflow(caller, userID, "canceled", `{"endpoint":"https://example.com","timeout":"10s"}`, "HEARTBEAT", 60, 3, true, uuid.NewString())
		if got != nil || status.Code(err) != code {
			t.Fatalf("canceled create=%+v err=%v, want %v", got, err, code)
		}
		assertCreateEffects(ctx, t, repo, userID, 0, 0, 0, 0)
	}
}

func TestIntegrationCreateWorkflowCanonicalReplayAndConflict(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository(t)
	userID := seedUser(ctx, t, repo.pg)
	key := uuid.NewString()
	created, err := repo.CreateWorkflow(ctx, userID, "heartbeat", `{"endpoint":"https://example.com","timeout":"10s"}`, "HEARTBEAT", 60, 3, true, key)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.BuildHash.Valid || created.IdempotencyReplay {
		t.Fatalf("created=%+v", created)
	}
	replay, err := repo.CreateWorkflow(ctx, strings.ToUpper(userID), "heartbeat", `{ "timeout": "10s", "endpoint": "https://example.com" }`, "HEARTBEAT", 60, 3, true, key)
	if err != nil {
		t.Fatal(err)
	}
	if replay.ID != created.ID || !replay.IdempotencyReplay || !replay.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("replay=%+v, created=%+v", replay, created)
	}
	assertCreateEffects(ctx, t, repo, userID, 1, 1, 1, 1)
	assertBuildEvent(ctx, t, repo, created.ID, created.Generation)
	conflict, err := repo.CreateWorkflow(ctx, userID, "changed", `{"endpoint":"https://example.com","timeout":"10s"}`, "HEARTBEAT", 60, 3, true, key)
	if conflict != nil || status.Code(err) != codes.AlreadyExists {
		t.Fatalf("conflict=%+v err=%v", conflict, err)
	}
	stored, err := repo.GetWorkflow(ctx, created.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != created.Name {
		t.Fatalf("conflicting reuse renamed workflow to %q, want %q", stored.Name, created.Name)
	}
	assertCreateEffects(ctx, t, repo, userID, 1, 1, 1, 1)
}

func TestIntegrationCreateWorkflowInterruptedInsertRollsBack(t *testing.T) {
	for _, stop := range []string{"cancel", "deadline", "server"} {
		t.Run(stop, func(t *testing.T) {
			ctx := context.Background()
			repo := newTestRepository(t)
			userID := seedUser(ctx, t, repo.pg)
			key := uuid.NewString()
			lock := lockWorkflowWrite("%INSERT INTO " + postgres.TableWorkflows + "%")
			caller, step := callerStopPlan(ctx, t, repo.pg, stop, lock)
			err := runWhileBlocked(caller, t, repo.pg, lock, step, func(commandCtx context.Context) error {
				got, createErr := repo.CreateWorkflow(commandCtx, userID, "interrupted-create", `{"image":"alpine:3.22.2"}`, "CONTAINER", 60, 3, true, key)
				if got != nil {
					t.Errorf("interrupted create returned workflow: %+v", got)
				}
				return createErr
			})
			want := codes.Internal
			if stop == "cancel" {
				want = codes.Canceled
			}
			if stop == "deadline" {
				want = codes.DeadlineExceeded
			}
			if status.Code(err) != want {
				t.Fatalf("interrupted create=%v, want %v", err, want)
			}
			assertCreateEffects(ctx, t, repo, userID, 0, 0, 0, 0)
			created, err := repo.CreateWorkflow(ctx, userID, "interrupted-create", `{"image":"alpine:3.22.2"}`, "CONTAINER", 60, 3, true, key)
			if err != nil || created == nil || created.IdempotencyReplay {
				t.Fatalf("retry=%+v err=%v", created, err)
			}
			assertCreateEffects(ctx, t, repo, userID, 1, 1, 1, 1)
			assertBuildEvent(ctx, t, repo, created.ID, created.Generation)
		})
	}
}
