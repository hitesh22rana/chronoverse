//nolint:testpackage // Real transaction failures must roll back authority and physical proof together.
package jobs

import (
	"context"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

// An isolated, fully migrated database lets schema/permission faults remain
// local to the fixture, never altering another test's shared database.
func isolatedContainerIntentRepository(t *testing.T) *Repository {
	t.Helper()
	repo := runtimeSelectionRepository(t)
	if _, err := repo.pg.Exec(t.Context(), "DROP TABLE runtime_nodes"); err != nil {
		t.Fatal(err)
	}
	var database string
	if err := repo.pg.QueryRow(t.Context(), "SELECT current_database()").Scan(&database); err != nil {
		t.Fatal(err)
	}
	dsn, err := url.Parse(testkit.PostgresDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	dsn.Path = "/" + database
	if err = postgres.Migrate(dsn.String()); err != nil {
		t.Fatal(err)
	}
	return repo
}

func seedIntentFailureFixture(t *testing.T, repo *Repository) *jobsmodel.ContainerIntentCommand {
	t.Helper()
	ctx := t.Context()
	user := testkit.SeedUser(ctx, t, repo.pg, "intent-fault-"+uuid.NewString()+"@test.local")
	workflow := testkit.SeedWorkflow(ctx, t, repo.pg, user, "intent-fault")
	job, lease := seedRunningJob(ctx, t, repo.pg, repo, workflow, user, t.Name())
	command := &jobsmodel.ContainerIntentCommand{JobID: job, LeaseToken: lease, Attempt: 1, WorkflowGeneration: 1, RuntimeInstance: "original-daemon", RequestHash: "immutable"}
	ensured, err := repo.UpdateContainerIntent(ctx, "ensure", command)
	if err != nil {
		t.Fatal(err)
	}
	command.IntentID = ensured.Intent.ID
	return command
}

func TestIntegrationContainerGrantFailureRollsBackOnWriteAndCommit(t *testing.T) {
	for _, deferred := range []bool{false, true} {
		name := "write failure"
		if deferred {
			name = "commit failure"
		}
		t.Run(name, func(t *testing.T) {
			repo := isolatedContainerIntentRepository(t)
			command := seedIntentFailureFixture(t, repo)
			remove := scopedJobFaultTrigger(t.Context(), t, repo.pg, "container_creation_intents", "UPDATE", deferred, map[string]string{"id": command.IntentID})
			result, err := repo.UpdateContainerIntent(t.Context(), "reserve_create", command)
			if result != nil || status.Code(err) != codes.Internal {
				t.Fatalf("failed transaction returned a send grant: %+v %v", result, err)
			}
			remove()
			stored, err := repo.UpdateContainerIntent(t.Context(), "get", command)
			if err != nil || stored.Intent.CreateReserved {
				t.Fatalf("failed grant remained committed: %+v %v", stored, err)
			}
		})
	}
}

func TestIntegrationContainerIntentUnavailableStorageHasNoSuccess(t *testing.T) {
	repo := isolatedContainerIntentRepository(t)
	command := seedIntentFailureFixture(t, repo)
	repo.pg.Close()
	if result, err := repo.UpdateContainerIntent(t.Context(), "get", command); result != nil || status.Code(err) != codes.Internal {
		t.Fatalf("closed pool returned intent: %+v %v", result, err)
	}
	if result, err := repo.ListContainerIntents(t.Context(), &jobsmodel.ContainerIntentCommand{}, false); result != nil || status.Code(err) != codes.Internal {
		t.Fatalf("closed pool returned listing: %+v %v", result, err)
	}
	if err := repo.RecordContainerCleanupReceipt(t.Context(), command.JobID, "container", command.RuntimeInstance, command.IntentID); err == nil {
		t.Fatal("closed pool recorded removal proof")
	}
}

//nolint:gocyclo // One transaction matrix proves every physical-proof write rolls back together.
func TestIntegrationContainerReceiptFailureRetainsHoldAndObligation(t *testing.T) {
	for _, table := range []string{"container_cleanup_receipts", "outbox_events", "runtime_capacity_holds", "runtime lock", "reconcile"} {
		t.Run(table, func(t *testing.T) {
			repo := isolatedContainerIntentRepository(t)
			req := seedIntentFailureFixture(t, repo)
			if _, err := repo.UpdateContainerIntent(t.Context(), "reserve_create", req); err != nil {
				t.Fatal(err)
			}
			req.ContainerID = strings.Repeat("a", 64)
			if _, err := repo.UpdateContainerIntent(t.Context(), "observe", req); err != nil {
				t.Fatal(err)
			}
			intent, err := repo.UpdateContainerIntent(t.Context(), "get", req)
			if err != nil {
				t.Fatal(err)
			}
			if err = repo.AttachJobContainer(t.Context(), req.JobID, req.LeaseToken, req.ContainerID, intent.Intent.RuntimeNodeID, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			if _, err = repo.UpdateContainerIntent(t.Context(), "reserve_start", req); err != nil {
				t.Fatal(err)
			}
			if _, err = repo.UpdateContainerIntent(t.Context(), "close", req); err != nil {
				t.Fatal(err)
			}
			event, field, value := "UPDATE", "intent_id", req.IntentID
			if table == "container_cleanup_receipts" {
				event = "INSERT"
			}
			if table == "outbox_events" {
				field = "event_key"
				value = cleanupEventKey(req.JobID, req.ContainerID, req.RuntimeInstance)
			}
			remove := func() {}
			switch table {
			case "runtime lock":
				if _, err = repo.pg.Exec(t.Context(), "DROP TABLE runtime_nodes CASCADE"); err != nil {
					t.Fatal(err)
				}
			case "reconcile":
				if _, err = repo.pg.Exec(t.Context(), `CREATE OR REPLACE FUNCTION reconcile_runtime_capacity(node TEXT) RETURNS VOID
LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'runtime accounting unavailable'; END; $$`); err != nil {
					t.Fatal(err)
				}
			default:
				remove = scopedJobFaultTrigger(t.Context(), t, repo.pg, table, event, false, map[string]string{field: value})
			}
			if err = repo.RecordContainerCleanupReceipt(t.Context(), req.JobID, req.ContainerID, req.RuntimeInstance, req.IntentID); err == nil {
				t.Fatal("partial removal proof was acknowledged")
			}
			remove()
			completed, err := repo.GetContainerCleanupReceipt(t.Context(), req.JobID, req.ContainerID, req.RuntimeInstance, req.IntentID)
			if err != nil || completed {
				t.Fatalf("failed cleanup persisted a receipt: completed=%v error=%v", completed, err)
			}
			var held int
			if err = repo.pg.QueryRow(t.Context(), "SELECT count(*) FROM runtime_capacity_holds WHERE intent_id=$1 AND released_at IS NULL", req.IntentID).Scan(&held); err != nil || held != 1 {
				t.Fatalf("failed proof released hold: held=%d error=%v", held, err)
			}
		})
	}
}

func TestIntegrationContainerMetricsStorageFailureDoesNotEmitZeros(t *testing.T) {
	repo := runtimeSelectionRepository(t)
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := provider.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	}()
	if err := repo.registerContainerIntentMetrics(provider.Meter("intent-failure")); err != nil {
		t.Fatal(err)
	}
	var data metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &data); err == nil {
		t.Fatal("missing durable schema was reported as a healthy zero backlog")
	}
	for _, scope := range data.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			t.Errorf("failed collection fabricated a measurement: %s", measurement.Name)
		}
	}
}

func TestIntegrationMissingWorkflowStorageCannotMutateClaimOrSettlement(t *testing.T) {
	repo := isolatedContainerIntentRepository(t)
	req := seedIntentFailureFixture(t, repo)
	var workflow string
	if err := repo.pg.QueryRow(t.Context(), "SELECT workflow_id::text FROM jobs WHERE id=$1", req.JobID).Scan(&workflow); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.pg.Exec(t.Context(), "ALTER TABLE workflows RENAME TO unavailable_workflows"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := repo.pg.Exec(t.Context(), "ALTER TABLE unavailable_workflows RENAME TO workflows"); err != nil {
			t.Error(err)
		}
	}()
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"claim", func() error {
			_, granted, _, err := repo.ClaimJob(t.Context(), req.JobID, workflow, "worker", uuid.NewString(), uuid.NewString(), time.Minute, 1)
			if granted {
				t.Fatal("failed admission granted a lease")
			}
			return err
		}},
		{"attachment", func() error {
			return repo.AttachJobContainer(t.Context(), req.JobID, req.LeaseToken, strings.Repeat("a", 64), "runtime", uuid.NewString())
		}},
		{"complete", func() error { return repo.CompleteJob(t.Context(), req.JobID, req.LeaseToken, uuid.NewString()) }},
		{"fail", func() error {
			return repo.FailJob(t.Context(), req.JobID, req.LeaseToken, "SYSTEM", "Unavailable", "runtime failed", "SYSTEM_ERROR", uuid.NewString())
		}},
		{"cancel claimed", func() error {
			return repo.CancelClaimedJob(t.Context(), req.JobID, req.LeaseToken, "WORKFLOW_TERMINATED", uuid.NewString())
		}},
		{"retry", func() error {
			return repo.ReleaseJobForRetry(t.Context(), req.JobID, req.LeaseToken, time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano), "Unavailable", "runtime failed", uuid.NewString())
		}},
		{"cancel workflow job", func() error {
			_, err := repo.CancelJob(t.Context(), req.JobID, uuid.NewString(), "WORKFLOW_TERMINATED")
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); status.Code(err) != codes.Internal {
				t.Fatalf("missing workflow storage acknowledged mutation: %v", err)
			}
		})
	}
	var jobStatus, lease string
	if err := repo.pg.QueryRow(t.Context(), "SELECT status::text,lease_token FROM jobs WHERE id=$1", req.JobID).Scan(&jobStatus, &lease); err != nil || jobStatus != "RUNNING" || lease != req.LeaseToken {
		t.Fatalf("failed authority check mutated source job: status=%q lease=%q error=%v", jobStatus, lease, err)
	}
}

func TestIntegrationContainerIntentDecodeFailureCannotReturnPartialInventory(t *testing.T) {
	repo := isolatedContainerIntentRepository(t)
	req := seedIntentFailureFixture(t, repo)
	if _, err := repo.pg.Exec(t.Context(), "ALTER TABLE container_creation_intents ALTER COLUMN outcome DROP NOT NULL"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.pg.Exec(t.Context(), "UPDATE container_creation_intents SET outcome=NULL WHERE id=$1", req.IntentID); err != nil {
		t.Fatal(err)
	}
	result, err := repo.ListContainerIntents(t.Context(), &jobsmodel.ContainerIntentCommand{}, false)
	if err == nil || result != nil {
		t.Fatalf("invalid inventory returned as success: %+v %v", result, err)
	}
}

func TestIntegrationContainerIntentMissingAndUnknownOperationsFailClosed(t *testing.T) {
	repo := isolatedContainerIntentRepository(t)
	req := seedIntentFailureFixture(t, repo)
	if result, err := repo.UpdateContainerIntent(t.Context(), "unsupported-operation", req); result != nil || status.Code(err) != codes.InvalidArgument {
		t.Fatalf("unregistered mutation accepted: %+v %v", result, err)
	}
	bad := *req
	bad.IntentID = "not-a-uuid"
	if result, err := repo.UpdateContainerIntent(t.Context(), "get", &bad); result != nil || status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid identity became a result: %+v %v", result, err)
	}
	missing := *req
	missing.IntentID = uuid.NewString()
	if result, err := repo.UpdateContainerIntent(t.Context(), "get", &missing); result != nil || status.Code(err) != codes.NotFound {
		t.Fatalf("missing identity became a result: %+v %v", result, err)
	}
}

func TestIntegrationLateCapacityFailureRollsBackTerminalSettlement(t *testing.T) {
	for _, operation := range []struct {
		name   string
		invoke func(context.Context, *Repository, string, string, string) error
	}{
		{"complete", completeJobTerminal},
		{"fail", failJobTerminal},
		{"cancel claimed", cancelClaimedTerminal},
		{
			"cancel command",
			func(ctx context.Context, repo *Repository, job, _, command string) error {
				_, err := repo.CancelJob(ctx, job, "WORKFLOW_TERMINATED", command)
				return err
			},
		},
	} {
		t.Run(operation.name, func(t *testing.T) {
			repo := isolatedContainerIntentRepository(t)
			fixture := seedClaimedJob(t.Context(), t, repo.pg, repo)
			before := readTerminalJobState(t.Context(), t, repo.pg, fixture.JobID)
			slots := readRuntimeRunningJobs(t.Context(), t, repo.pg, before.RuntimeNodeID.String)
			var original string
			if err := repo.pg.QueryRow(t.Context(), "SELECT pg_get_functiondef('reconcile_runtime_capacity(text)'::regprocedure)").Scan(&original); err != nil {
				t.Fatal(err)
			}
			saved := strings.Replace(original, "reconcile_runtime_capacity", "saved_reconcile_runtime_capacity", 1)
			if _, err := repo.pg.Exec(t.Context(), saved); err != nil {
				t.Fatal(err)
			}
			if _, err := repo.pg.Exec(t.Context(), "CREATE SEQUENCE capacity_fault_call"); err != nil {
				t.Fatal(err)
			}
			// The trigger's accounting succeeds; a later failure must still roll back
			// the job, outbox and command ledger as one settlement.
			if _, err := repo.pg.Exec(t.Context(), `CREATE OR REPLACE FUNCTION reconcile_runtime_capacity(node TEXT) RETURNS VOID
LANGUAGE plpgsql AS $$
BEGIN
 PERFORM saved_reconcile_runtime_capacity(node);
 IF nextval('capacity_fault_call') = 2 THEN
  RAISE EXCEPTION 'late runtime accounting failure';
 END IF;
END; $$`); err != nil {
				t.Fatal(err)
			}
			command := "late-capacity-" + fixtureTag()
			err := operation.invoke(t.Context(), repo, fixture.JobID, fixture.LeaseToken, command)
			if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "late runtime accounting failure") {
				t.Fatalf("late accounting failure acknowledged: %v", err)
			}
			after := readTerminalJobState(t.Context(), t, repo.pg, fixture.JobID)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("failed settlement changed job: before=%+v after=%+v", before, after)
			}
			if got := readRuntimeRunningJobs(t.Context(), t, repo.pg, before.RuntimeNodeID.String); got != slots {
				t.Fatalf("failed settlement changed slots: got=%d want=%d", got, slots)
			}
			assertNoOutboxEffect(t.Context(), t, repo.pg, fixture)
			if _, err := repo.pg.Exec(t.Context(), original); err != nil {
				t.Fatal(err)
			}
			if err := operation.invoke(t.Context(), repo, fixture.JobID, fixture.LeaseToken, command); err != nil {
				t.Fatalf("same-command retry failed: %v", err)
			}
		})
	}
}
