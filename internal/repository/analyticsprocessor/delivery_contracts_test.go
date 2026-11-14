//nolint:testpackage // Exercises internal event dispatch and SQL delivery contracts.
package analyticsprocessor

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	analyticsmodel "github.com/hitesh22rana/chronoverse/internal/model/analytics"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestIntegrationAnalyticsDeliveryContracts(t *testing.T) {
	for _, kind := range []analyticsmodel.EventType{analyticsmodel.EventTypeJobs, analyticsmodel.EventTypeLogs, analyticsmodel.EventTypeWorkflows} {
		t.Run(string(kind), func(t *testing.T) {
			pg := testkit.Postgres(t)
			repo := &Repository{pg: pg, tp: otel.Tracer("analytics-delivery-test")}
			user := testkit.SeedUser(t.Context(), t, pg, fmt.Sprintf("delivery-%s@chronoverse.test", t.Name()))
			workflow := uuid.NewString()
			event := &analyticsmodel.AnalyticEvent{UserID: user, WorkflowID: workflow, EventType: kind}
			process := repo.processJobsEvent
			payload, changed := `{"job_execution_duration":11}`, `{"job_execution_duration":90}`
			switch kind {
			case analyticsmodel.EventTypeJobs:
				// Jobs use the initial processor and payload.
			case analyticsmodel.EventTypeLogs:
				process = repo.processLogsEvent
				payload, changed = `{"count":7}`, `{"count":90}`
			case analyticsmodel.EventTypeWorkflows:
				process = repo.processWorkflowsEvent
				payload, changed = `{"kind":"CONTAINER"}`, `{"kind":"HEARTBEAT"}`
			}
			for _, data := range []json.RawMessage{nil, json.RawMessage(`{"bad":`), json.RawMessage(`[]`)} {
				event.Data = data
				if err := process(t.Context(), event); status.Code(err) != codes.InvalidArgument {
					t.Fatalf("invalid data %s: %v", data, err)
				}
			}
			assertAnalyticsState(t, repo, user, workflow, "", 0, 0, 0, false)
			event.Data = json.RawMessage(payload)
			if err := process(t.Context(), event); status.Code(err) != codes.InvalidArgument {
				t.Fatalf("missing event key: %v", err)
			}
			assertAnalyticsState(t, repo, user, workflow, "", 0, 0, 0, false)
			// A keyed duplicate with a conflicting payload is ignored by event identity.
			event.EventKey = uuid.NewString()
			if err := process(t.Context(), event); err != nil {
				t.Fatal(err)
			}
			event.Data = json.RawMessage(changed)
			if err := process(t.Context(), event); err != nil {
				t.Fatal(err)
			}
			wantKind, jobs, logs, duration := "", int64(0), int64(0), int64(0)
			switch kind {
			case analyticsmodel.EventTypeJobs:
				jobs, duration = 1, 11
			case analyticsmodel.EventTypeLogs:
				logs = 7
			case analyticsmodel.EventTypeWorkflows:
				wantKind = "CONTAINER"
			}
			assertAnalyticsState(t, repo, user, workflow, wantKind, jobs, logs, duration, true)
			assertAnalyticsLedgerCount(t, repo, event.EventKey, 1)
			assertAnalyticsFailureRetry(t, repo, event, process, user, payload, wantKind)
		})
	}
}

func assertAnalyticsState(t *testing.T, repo *Repository, user, workflow, wantKind string, wantJobs, wantLogs, wantDuration int64, exists bool) {
	t.Helper()
	var count int
	if err := repo.pg.QueryRow(t.Context(), `SELECT count(*) FROM analytics WHERE user_id=$1 AND workflow_id=$2`, user, workflow).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if !exists {
		if count != 0 {
			t.Fatalf("unexpected analytics rows: %d", count)
		}
		return
	}
	var kind string
	var jobs, logs, duration int64
	query := `SELECT kind,jobs_count,logs_count,total_job_execution_duration FROM analytics WHERE user_id=$1 AND workflow_id=$2`
	if err := repo.pg.QueryRow(t.Context(), query, user, workflow).Scan(&kind, &jobs, &logs, &duration); err != nil {
		t.Fatal(err)
	}
	if count != 1 || kind != wantKind || jobs != wantJobs || logs != wantLogs || duration != wantDuration {
		t.Fatalf("state=(%d,%q,%d,%d,%d), want (1,%q,%d,%d,%d)", count, kind, jobs, logs, duration, wantKind, wantJobs, wantLogs, wantDuration)
	}
}

func TestIntegrationAnalyticsRecordDispatchContracts(t *testing.T) {
	pg := testkit.Postgres(t)
	repo := &Repository{pg: pg, tp: otel.Tracer("analytics-dispatch-test")}
	for _, data := range [][]byte{[]byte(`{`), []byte(`{"EventType":"UNKNOWN"}`)} {
		if err := repo.processRecord(t.Context(), &kgo.Record{Value: data}); err != nil {
			t.Fatalf("discarded record: %v", err)
		}
	}
	user := testkit.SeedUser(t.Context(), t, pg, "dispatch-contract@chronoverse.test")
	workflow := uuid.NewString()
	for _, kind := range []analyticsmodel.EventType{analyticsmodel.EventTypeJobs, analyticsmodel.EventTypeLogs, analyticsmodel.EventTypeWorkflows} {
		data := mustAnalyticEvent(t, uuid.NewString(), user, workflow, kind, map[string]any{"job_execution_duration": 4, "count": 2, "kind": "CONTAINER"})
		if err := repo.processRecord(t.Context(), &kgo.Record{Value: data}); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		canceledRecord := mustAnalyticEvent(t, uuid.NewString(), user, workflow, kind, map[string]any{"job_execution_duration": 4, "count": 2, "kind": "CONTAINER"})
		if err := repo.processRecord(ctx, &kgo.Record{Value: canceledRecord}); err == nil {
			t.Fatalf("canceled %s record succeeded", kind)
		}
	}
	assertAnalyticsState(t, repo, user, workflow, "CONTAINER", 1, 2, 4, true)
}

func assertAnalyticsFailureRetry(
	t *testing.T, repo *Repository, event *analyticsmodel.AnalyticEvent,
	process func(context.Context, *analyticsmodel.AnalyticEvent) error, user, payload, wantKind string,
) {
	t.Helper()
	var ledger int
	var jobs, logs, duration int64
	// FK failure must roll back the CTE's processed-event insertion as well.
	failedKey := uuid.NewString()
	event.EventKey, event.UserID = failedKey, uuid.NewString()
	if err := process(t.Context(), event); status.Code(err) != codes.Internal {
		t.Fatalf("failed SQL key=%q: %v", failedKey, err)
	}

	if err := repo.pg.QueryRow(t.Context(), `SELECT count(*) FROM processed_events WHERE consumer='analytics-processor' AND event_key=$1`, failedKey).Scan(&ledger); err != nil || ledger != 0 {
		t.Fatalf("failed ledger=%d error=%v", ledger, err)
	}
	event.UserID, event.WorkflowID, event.Data = user, uuid.NewString(), json.RawMessage(payload)
	if err := process(t.Context(), event); err != nil {
		t.Fatalf("retry after SQL failure: %v", err)
	}
	switch event.EventType {
	case analyticsmodel.EventTypeJobs:
		jobs, duration = 1, 11
	case analyticsmodel.EventTypeLogs:
		logs = 7
	case analyticsmodel.EventTypeWorkflows:
		jobs, logs, duration = 0, 0, 0
	}
	assertAnalyticsState(t, repo, user, event.WorkflowID, wantKind, jobs, logs, duration, true)
}

func assertAnalyticsLedgerCount(t *testing.T, repo *Repository, key string, want int) {
	t.Helper()
	var count int
	if err := repo.pg.QueryRow(t.Context(), `SELECT count(*) FROM processed_events WHERE consumer='analytics-processor' AND event_key=$1`, key).Scan(&count); err != nil || count != want {
		t.Fatalf("ledger=%d error=%v, want %d", count, err, want)
	}
}
