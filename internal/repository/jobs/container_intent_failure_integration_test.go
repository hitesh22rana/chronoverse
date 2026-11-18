//nolint:testpackage // Real transaction failures must roll back authority and physical proof together.
package jobs

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
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
	ensured, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationEnsure, command)
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
			result, err := repo.UpdateContainerIntent(t.Context(), jobsmodel.ContainerIntentOperationReserveCreate, command)
			if result != nil || status.Code(err) != codes.Internal {
				t.Fatalf("failed transaction returned a send grant: %+v %v", result, err)
			}
			remove()
			stored, err := repo.UpdateContainerIntent(t.Context(), jobsmodel.ContainerIntentOperationGet, command)
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
	if result, err := repo.UpdateContainerIntent(t.Context(), jobsmodel.ContainerIntentOperationGet, command); result != nil || status.Code(err) != codes.Internal {
		t.Fatalf("closed pool returned intent: %+v %v", result, err)
	}
	for _, claim := range []bool{false, true} {
		if result, err := repo.ListContainerIntents(t.Context(), &jobsmodel.ContainerIntentCommand{}, claim); result != nil || status.Code(err) != codes.Internal {
			t.Fatalf("closed pool returned listing: %+v %v", result, err)
		}
	}
	if err := repo.RecordContainerCleanupReceipt(t.Context(), jobsmodel.CleanupIdentity{
		JobID:           command.JobID,
		ContainerID:     "container",
		RuntimeInstance: command.RuntimeInstance,
		IntentID:        command.IntentID,
	}); err == nil {
		t.Fatal("closed pool recorded removal proof")
	}
}

//nolint:gocyclo // One transaction matrix proves every physical-proof write rolls back together.
func TestIntegrationContainerReceiptFailureRetainsHoldAndObligation(t *testing.T) {
	for _, table := range []string{"container_cleanup_receipts", "outbox_events", "runtime_capacity_holds", "runtime lock", "reconcile"} {
		t.Run(table, func(t *testing.T) {
			repo := isolatedContainerIntentRepository(t)
			req := seedIntentFailureFixture(t, repo)
			if _, err := repo.UpdateContainerIntent(t.Context(), jobsmodel.ContainerIntentOperationReserveCreate, req); err != nil {
				t.Fatal(err)
			}
			req.ContainerID = strings.Repeat("a", 64)
			if _, err := repo.UpdateContainerIntent(t.Context(), jobsmodel.ContainerIntentOperationObserve, req); err != nil {
				t.Fatal(err)
			}
			intent, err := repo.UpdateContainerIntent(t.Context(), jobsmodel.ContainerIntentOperationGet, req)
			if err != nil {
				t.Fatal(err)
			}
			if err = repo.AttachJobContainer(t.Context(), req.JobID, req.LeaseToken, req.ContainerID, intent.Intent.RuntimeNodeID, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			if _, err = repo.UpdateContainerIntent(t.Context(), jobsmodel.ContainerIntentOperationReserveStart, req); err != nil {
				t.Fatal(err)
			}
			if _, err = repo.UpdateContainerIntent(t.Context(), jobsmodel.ContainerIntentOperationClose, req); err != nil {
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
			if err = repo.RecordContainerCleanupReceipt(t.Context(), jobsmodel.CleanupIdentity{
				JobID:           req.JobID,
				ContainerID:     req.ContainerID,
				RuntimeInstance: req.RuntimeInstance,
				IntentID:        req.IntentID,
			}); err == nil {
				t.Fatal("partial removal proof was acknowledged")
			}
			remove()
			completed, err := repo.GetContainerCleanupReceipt(t.Context(), jobsmodel.CleanupIdentity{
				JobID:           req.JobID,
				ContainerID:     req.ContainerID,
				RuntimeInstance: req.RuntimeInstance,
				IntentID:        req.IntentID,
			})
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
	if _, err := repo.pg.Exec(t.Context(), "UPDATE container_creation_intents SET closed_at=now() AT TIME ZONE 'utc' WHERE id=$1", req.IntentID); err != nil {
		t.Fatal(err)
	}
	for _, claim := range []bool{false, true} {
		result, err := repo.ListContainerIntents(t.Context(), &jobsmodel.ContainerIntentCommand{}, claim)
		if err == nil || result != nil {
			t.Fatalf("invalid inventory returned as success: %+v %v", result, err)
		}
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
	if result, err := repo.UpdateContainerIntent(t.Context(), jobsmodel.ContainerIntentOperationGet, &bad); result != nil || status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid identity became a result: %+v %v", result, err)
	}
	missing := *req
	missing.IntentID = uuid.NewString()
	if result, err := repo.UpdateContainerIntent(t.Context(), jobsmodel.ContainerIntentOperationGet, &missing); result != nil || status.Code(err) != codes.NotFound {
		t.Fatalf("missing identity became a result: %+v %v", result, err)
	}
}

func TestIntegrationCapacityFailureRollsBackTerminalSettlement(t *testing.T) {
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
				_, err := repo.CancelJob(ctx, job, command, "WORKFLOW_TERMINATED")
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
			// A failure after the trigger updates capacity must roll back the
			// job, outbox and command ledger as one settlement.
			if _, err := repo.pg.Exec(t.Context(), `CREATE OR REPLACE FUNCTION reconcile_runtime_capacity(node TEXT) RETURNS VOID
LANGUAGE plpgsql AS $$
BEGIN
 PERFORM saved_reconcile_runtime_capacity(node);
 IF nextval('capacity_fault_call') = 1 THEN
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

//nolint:gocyclo // One durable identity exercises response replay, conflicting observation and physical-proof ownership.
func TestIntegrationContainerIdentityProducersAndConflicts(t *testing.T) {
	repo := isolatedContainerIntentRepository(t)
	repo.cfg.FetchLimit = 10
	ctx := t.Context()
	first := seedIntentFailureFixture(t, repo)
	if _, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationReserveCreate, first); err != nil {
		t.Fatal(err)
	}
	first.ContainerID = strings.Repeat("c", 64)
	if _, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationObserve, first); err != nil {
		t.Fatal(err)
	}
	intent, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationGet, first)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.AttachJobContainer(ctx, first.JobID, first.LeaseToken, first.ContainerID, intent.Intent.RuntimeNodeID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationReserveStart, first); err != nil {
		t.Fatal(err)
	}
	listed, err := repo.ListJobs(ctx, intent.Intent.WorkflowID, intent.Intent.UserID, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Jobs) != 1 {
		t.Fatalf("unexpected listed jobs: %+v", listed)
	}
	job := listed.ToProto(true).GetJobs()[0]
	if job.GetRuntimeInstance() != first.RuntimeInstance || job.GetIntentId() != first.IntentID {
		t.Fatalf("list omitted provenance: %+v", job)
	}
	public := listed.ToProto(false).GetJobs()[0]
	if public.GetRuntimeInstance() != "" || public.GetIntentId() != "" {
		t.Fatal("public list exposed internal provenance")
	}
	second := seedIntentFailureFixture(t, repo)
	if _, err = repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationReserveCreate, second); err != nil {
		t.Fatal(err)
	}
	second.ContainerID = first.ContainerID
	result, observeErr := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationObserve, second)
	if observeErr == nil || result != nil {
		t.Fatalf("cross-intent physical ID accepted: %+v %v", result, observeErr)
	}
	lookup := *second
	lookup.ContainerID = ""
	untouched, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationGet, &lookup)
	if err != nil || untouched.Intent.ContainerID != "" {
		t.Fatalf("failed observation changed identity: %+v %v", untouched, err)
	}
	command := uuid.NewString()
	for range 2 {
		canceled, err := repo.CancelJob(ctx, first.JobID, command, "WORKFLOW_TERMINATED")
		if err != nil {
			t.Fatal(err)
		}
		snapshot := canceled.ToProto()
		if snapshot.GetRuntimeInstance() != first.RuntimeInstance || snapshot.GetIntentId() != first.IntentID || snapshot.GetContainerId() != first.ContainerID {
			t.Fatalf("cancel/replay omitted provenance: %+v", snapshot)
		}
	}
	// An inconsistent pre-existing receipt cannot acknowledge another intent's hold.
	if _, err := repo.pg.Exec(ctx, `
INSERT INTO container_cleanup_receipts(job_id,container_id,runtime_instance,intent_id)
VALUES($1,$2,$3,$4)`, first.JobID, first.ContainerID, first.RuntimeInstance, second.IntentID); err != nil {
		t.Fatal(err)
	}
	identity := jobsmodel.CleanupIdentity{
		JobID:           first.JobID,
		ContainerID:     first.ContainerID,
		RuntimeInstance: first.RuntimeInstance,
		IntentID:        first.IntentID,
	}
	if err := repo.RecordContainerCleanupReceipt(ctx, identity); err == nil {
		t.Fatal("conflicting receipt released authority")
	}
	var held bool
	if err := repo.pg.QueryRow(ctx, "SELECT released_at IS NULL FROM runtime_capacity_holds WHERE intent_id=$1", first.IntentID).Scan(&held); err != nil || !held {
		t.Fatalf("conflicting receipt released hold: held=%v error=%v", held, err)
	}
	if _, err := repo.pg.Exec(ctx, "DELETE FROM container_cleanup_receipts WHERE job_id=$1", first.JobID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := repo.RecordContainerCleanupReceipt(ctx, identity); err != nil {
			t.Fatal(err)
		}
	}
}

func discoveryWireFault(t *testing.T, repo *Repository, drop func(byte, []byte) bool) *Repository {
	t.Helper()
	cfg, err := pgx.ParseConfig(testkit.PostgresDSN(t))
	require.NoError(t, err)
	var database string
	require.NoError(t, repo.pg.QueryRow(t.Context(), "SELECT current_database()").Scan(&database))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var workers sync.WaitGroup
	var mu sync.Mutex
	var connections []net.Conn
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			client, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			server, dialErr := net.DialTimeout("tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), time.Second)
			if dialErr != nil {
				_ = client.Close()
				continue
			}
			mu.Lock()
			connections = append(connections, client, server)
			mu.Unlock()
			workers.Add(2)
			go func() {
				defer workers.Done()
				//nolint:errcheck // Fault proxy intentionally terminates connections.
				_, _ = io.Copy(server, client)
				_ = server.Close()
			}()
			go func() {
				defer workers.Done()
				defer client.Close()
				defer server.Close()
				for {
					var header [5]byte
					if _, readErr := io.ReadFull(server, header[:]); readErr != nil {
						return
					}
					length := int(binary.BigEndian.Uint32(header[1:])) - 4
					if length < 0 || length > 16<<20 {
						return
					}
					body := make([]byte, length)
					if _, readErr := io.ReadFull(server, body); readErr != nil {
						return
					}
					if drop(header[0], body) {
						return
					}
					if _, writeErr := client.Write(header[:]); writeErr != nil {
						return
					}
					if _, writeErr := client.Write(body); writeErr != nil {
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-acceptDone
		mu.Lock()
		for _, conn := range connections {
			_ = conn.Close()
		}
		mu.Unlock()
		workers.Wait()
	})
	address, ok := listener.Addr().(*net.TCPAddr)
	require.True(t, ok)
	port := address.Port
	pg, err := postgres.New(t.Context(), &postgres.Config{
		Host: "127.0.0.1", Port: port, User: cfg.User,
		Password: cfg.Password, Database: database, MaxConns: 3, TLSConfig: &postgres.TLSConfig{},
	})
	require.NoError(t, err)
	t.Cleanup(pg.Close)
	faulty := *repo
	faulty.pg = pg
	return &faulty
}

func TestIntegrationDiscoveryLostCommitCannotGrantFallback(t *testing.T) {
	repo := isolatedContainerIntentRepository(t)
	fixture := seedRetainedCleanupOn(t, repo.pg)
	require.NoError(t, func() error {
		_, err := repo.pg.Exec(t.Context(), `
			UPDATE container_creation_intents
			SET
				closed_at = now() AT TIME ZONE 'utc'
			WHERE
				id = $1
		`, fixture.IntentID)
		return err
	}())
	faulty := discoveryWireFault(t, repo, func(kind byte, body []byte) bool {
		return kind == 'C' && string(body) == "COMMIT\x00"
	})
	result, err := faulty.ListContainerIntents(t.Context(), &jobsmodel.ContainerIntentCommand{BatchSize: 1}, true)
	require.Error(t, err)
	require.Nil(t, result, "uncertain commit must expose no tokens")
	var token string
	var next bool
	require.NoError(t, repo.pg.QueryRow(t.Context(), `SELECT inspection_token::text FROM container_creation_intents WHERE id=$1`, fixture.IntentID).Scan(&token))
	require.NotEmpty(t, token)
	require.NoError(t, repo.pg.QueryRow(t.Context(), `SELECT next_urgent FROM container_discovery_turn`).Scan(&next))
	require.False(t, next, "token and turn committed together")
	result, err = repo.ListContainerIntents(t.Context(), &jobsmodel.ContainerIntentCommand{BatchSize: 1}, true)
	require.NoError(t, err)
	require.Empty(t, result, "a failed response does not authorize another grant")
	_, err = repo.pg.Exec(t.Context(), `
		UPDATE container_creation_intents
		SET
			inspection_until = now() AT TIME ZONE 'utc' - interval '1 second'
		WHERE
			id = $1
	`, fixture.IntentID)
	require.NoError(t, err)
	result, err = repo.ListContainerIntents(t.Context(), &jobsmodel.ContainerIntentCommand{BatchSize: 1}, true)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.NotEqual(t, token, result[0].Token, "expired claim recovers with fresh authority")
}

func TestIntegrationDiscoveryClaimFailuresRollBackTurnAndTokens(t *testing.T) {
	for _, deferred := range []bool{false, true} {
		t.Run(fmt.Sprint(deferred), func(t *testing.T) {
			repo := isolatedContainerIntentRepository(t)
			fixture := seedRetainedCleanupOn(t, repo.pg)
			_, err := repo.pg.Exec(t.Context(), `
				UPDATE container_creation_intents
				SET
					closed_at = now() AT TIME ZONE 'utc'
				WHERE
					id = $1
			`, fixture.IntentID)
			require.NoError(t, err)
			remove := scopedJobFaultTrigger(t.Context(), t, repo.pg, "container_creation_intents", "UPDATE", deferred, map[string]string{"id": fixture.IntentID})
			result, err := repo.ListContainerIntents(t.Context(), &jobsmodel.ContainerIntentCommand{BatchSize: 1}, true)
			require.Error(t, err)
			require.Nil(t, result)
			remove()
			var unclaimed, next bool
			require.NoError(t, repo.pg.QueryRow(t.Context(), `SELECT inspection_token IS NULL FROM container_creation_intents WHERE id=$1`, fixture.IntentID).Scan(&unclaimed))
			require.NoError(t, repo.pg.QueryRow(t.Context(), `SELECT next_urgent FROM container_discovery_turn`).Scan(&next))
			require.True(t, unclaimed)
			require.True(t, next)
		})
	}
}

func TestIntegrationObservationFailureRollsBackPromotionAndProof(t *testing.T) {
	for _, target := range []string{"primary identity", "cleanup obligation"} {
		t.Run(target, func(t *testing.T) {
			repo := isolatedContainerIntentRepository(t)
			command := seedIntentFailureFixture(t, repo)
			command.ContainerID = strings.Repeat("b", 64)
			var remove func()
			if target == "primary identity" {
				remove = scopedJobFaultTrigger(t.Context(), t, repo.pg, "container_creation_intents", "UPDATE", false, map[string]string{"id": command.IntentID, "container_id": command.ContainerID})
			} else {
				_, err := repo.UpdateContainerIntent(t.Context(), jobsmodel.ContainerIntentOperationClose, command)
				require.NoError(t, err)
				remove = scopedJobFaultTrigger(t.Context(), t, repo.pg, "outbox_events", "INSERT", false,
					map[string]string{"event_key": cleanupEventKey(command.JobID, command.ContainerID, command.RuntimeInstance)})
			}
			_, err := repo.pg.Exec(t.Context(), `
				UPDATE container_creation_intents
				SET
					inspection_urgent = FALSE,
					next_inspection_at = '2099-01-01'
				WHERE
					id = $1
			`, command.IntentID)
			require.NoError(t, err)
			result, err := repo.UpdateContainerIntent(t.Context(), jobsmodel.ContainerIntentOperationObserve, command)
			require.Error(t, err)
			require.Nil(t, result)
			remove()
			var absent, preserved bool
			require.NoError(t, repo.pg.QueryRow(t.Context(), `
				SELECT
					NOT EXISTS (
						SELECT
							1
						FROM
							container_materializations
						WHERE
							intent_id = $1
					),
					NOT inspection_urgent
					AND next_inspection_at = '2099-01-01'
				FROM
					container_creation_intents
				WHERE
					id = $1
			`, command.IntentID).Scan(&absent, &preserved))
			require.True(t, absent, "failed observation must not fabricate cleanup evidence")
			require.True(t, preserved, "failed transaction must roll back urgency promotion")
		})
	}
}

func TestIntegrationDiscoveryTurnFailureCannotReturnTokens(t *testing.T) {
	repo := isolatedContainerIntentRepository(t)
	fixture := seedRetainedCleanupOn(t, repo.pg)
	_, err := repo.pg.Exec(t.Context(), `
		UPDATE container_creation_intents
		SET
			closed_at = now() AT TIME ZONE 'utc'
		WHERE
			id = $1
	`, fixture.IntentID)
	require.NoError(t, err)
	remove := scopedJobFaultTrigger(t.Context(), t, repo.pg, "container_discovery_turn", "UPDATE", false, map[string]string{"id": "true"})
	result, err := repo.ListContainerIntents(t.Context(), &jobsmodel.ContainerIntentCommand{BatchSize: 1}, true)
	require.Error(t, err)
	require.Nil(t, result)
	remove()
	var unclaimed bool
	require.NoError(t, repo.pg.QueryRow(t.Context(), `SELECT inspection_token IS NULL FROM container_creation_intents WHERE id=$1`, fixture.IntentID).Scan(&unclaimed))
	require.True(t, unclaimed, "turn update failure rolled back the executable lease")
	_, err = repo.pg.Exec(t.Context(), `ALTER TABLE container_discovery_turn RENAME COLUMN next_urgent TO unavailable_next_urgent`)
	require.NoError(t, err)
	result, err = repo.ListContainerIntents(t.Context(), &jobsmodel.ContainerIntentCommand{}, true)
	require.Error(t, err)
	require.Nil(t, result)
}
