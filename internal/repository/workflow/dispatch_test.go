//nolint:testpackage // Exercises private worker dispatch contracts.
package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

func TestWorkflowDispatchPreservesActionFailures(t *testing.T) {
	t.Parallel()
	for _, action := range []workflowsmodel.Action{
		workflowsmodel.ActionBuild, workflowsmodel.ActionReschedule, workflowsmodel.ActionTerminate,
		workflowsmodel.ActionJobCompleted, workflowsmodel.ActionJobFailed,
	} {
		t.Run(action.ToString(), func(t *testing.T) {
			t.Parallel()
			failure := status.Error(codes.Unavailable, "authorization unavailable")
			calls := 0
			repo := &Repository{auth: testAuth{issueToken: func(context.Context, string) (string, error) { calls++; return "", failure }}}
			event := lifecycleEvent(action)
			event.JobID = "job-1"
			if err := repo.runTargetWorkflow(t.Context(), event); !errors.Is(err, failure) {
				t.Fatalf("dispatch error = %v, want original failure", err)
			}
			if calls != 1 {
				t.Fatalf("authorization calls = %d, want 1", calls)
			}
		})
	}
	t.Run("delete locks the event workflow", func(t *testing.T) {
		t.Parallel()
		lock := &lifecycleLock{}
		repo := &Repository{rdb: lock}
		if err := repo.runTargetWorkflow(t.Context(), lifecycleEvent(workflowsmodel.ActionDelete)); status.Code(err) != codes.Aborted {
			t.Fatalf("delete error = %v", err)
		}
		if len(lock.keys) != 1 || lock.keys[0] != "workflow:DELETE:workflow-1" {
			t.Fatalf("delete lock keys = %v", lock.keys)
		}
		if len(lock.released) != 0 {
			t.Fatalf("unowned locks released: %v", lock.released)
		}
	})
	t.Run("unknown action", func(t *testing.T) {
		t.Parallel()
		err := (&Repository{}).runTargetWorkflow(t.Context(), lifecycleEvent(workflowsmodel.Action("UNKNOWN")))
		if status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "UNKNOWN") {
			t.Fatalf("unknown action error = %v", err)
		}
	})
}

func TestWorkflowProcessRecordAcknowledgementContract(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, payload string
		code          codes.Code
		auth          bool
	}{
		{name: "malformed JSON is discarded", payload: "{", code: codes.OK},
		{name: "wrong JSON shape is discarded", payload: "[]", code: codes.OK},
		{name: "unknown event is returned", payload: `{"action":"UNKNOWN"}`, code: codes.InvalidArgument},
		{name: "retryable failure is returned", code: codes.Unavailable, auth: true},
		{name: "internal failure is returned", code: codes.Internal, auth: true},
		{name: "terminal failure is returned", code: codes.PermissionDenied, auth: true},
		{name: "successful stale event is acknowledged", code: codes.OK, auth: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo, _ := lifecycleRepository(lifecycleWorkflow())
			repo.tp = otel.Tracer("workflow-dispatch-test")
			failure := status.Error(tc.code, "event failure")
			calls := 0
			repo.auth = testAuth{issueToken: func(context.Context, string) (string, error) { calls++; return "token", failure }}
			payload := []byte(tc.payload)
			if tc.auth {
				event := lifecycleEvent(workflowsmodel.ActionReschedule)
				event.Generation = 2
				var err error
				payload, err = json.Marshal(event)
				if err != nil {
					t.Fatal(err)
				}
			}
			err := repo.processRecord(t.Context(), &kgo.Record{Topic: "workflow", Partition: 2, Offset: 17, Key: []byte("workflow-1"), Value: payload})
			if status.Code(err) != tc.code {
				t.Fatalf("record error = %v, want %s", err, tc.code)
			}
			if failure != nil && tc.auth && !errors.Is(err, failure) {
				t.Fatalf("record replaced original error: %v", err)
			}
			if tc.auth && calls != 1 || !tc.auth && calls != 0 {
				t.Fatalf("authorization calls = %d", calls)
			}
		})
	}
}

func TestWorkflowDispatchUsesActionIdentity(t *testing.T) {
	t.Parallel()
	for _, action := range []workflowsmodel.Action{workflowsmodel.ActionBuild, workflowsmodel.ActionReschedule, workflowsmodel.ActionTerminate} {
		t.Run(action.ToString(), func(t *testing.T) {
			t.Parallel()
			flow := lifecycleWorkflow()
			if action == workflowsmodel.ActionBuild {
				flow.BuildStatus = workflowsmodel.WorkflowBuildStatusQueued.ToString()
			}
			repo, lock := lifecycleRepository(flow)
			lock.acquired = false
			if err := repo.runTargetWorkflow(t.Context(), lifecycleEvent(action)); status.Code(err) != codes.Aborted {
				t.Fatalf("dispatch error = %v", err)
			}
			if len(lock.keys) != 1 || lock.keys[0] != "workflow:"+action.ToString()+":workflow-1" {
				t.Fatalf("dispatch lock = %v", lock.keys)
			}
		})
	}
	for _, action := range []workflowsmodel.Action{workflowsmodel.ActionJobCompleted, workflowsmodel.ActionJobFailed} {
		t.Run(action.ToString(), func(t *testing.T) {
			t.Parallel()
			repo, _ := lifecycleRepository(lifecycleWorkflow())
			var got string
			client := &testWorkflowsClient{workflow: lifecycleWorkflow()}
			client.incrementFailures = func(_ context.Context, req *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest) (*workflowspb.IncrementWorkflowConsecutiveJobFailuresCountResponse, error) {
				got = "increment:" + req.GetId() + ":" + req.GetUserId() + ":" + req.GetJobId()
				return &workflowspb.IncrementWorkflowConsecutiveJobFailuresCountResponse{}, nil
			}
			repo.svc.Workflows = resetRecordingWorkflowsClient{testWorkflowsClient: client, resetCounters: func(_ context.Context, req *workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest) error {
				got = "reset:" + req.GetId() + ":" + req.GetUserId() + ":" + req.GetJobId()
				return nil
			}}
			event := lifecycleEvent(action)
			event.JobID = "job-1"
			if err := repo.runTargetWorkflow(t.Context(), event); err != nil {
				t.Fatal(err)
			}
			want := "reset:workflow-1:user-1:job-1"
			if action == workflowsmodel.ActionJobFailed {
				want = "increment:workflow-1:user-1:job-1"
			}
			if got != want {
				t.Fatalf("terminal command=%q want %q", got, want)
			}
		})
	}
}
