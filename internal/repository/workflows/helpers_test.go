//nolint:testpackage // Tests the unexported stale-event helper without widening production API.
package workflows

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
)

func TestWorkflowRequestHashCanonicalizesPayload(t *testing.T) {
	t.Parallel()

	canonicalA, err := workflowRequestHash(map[string]any{"payload": `{ "image": "alpine" }`, "name": "workflow"})
	if err != nil {
		t.Fatalf("workflowRequestHashes() error = %v", err)
	}
	canonicalB, err := workflowRequestHash(map[string]any{"payload": `{"image":"alpine"}`, "name": "workflow"})
	if err != nil {
		t.Fatalf("workflowRequestHashes() canonical input error = %v", err)
	}
	if canonicalA != canonicalB {
		t.Fatalf("canonical hashes differ: %q != %q", canonicalA, canonicalB)
	}
}

func TestEscapeLikeQueryNeutralizesWildcards(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		`%_%`:   `\%\_\%`,
		`100%`:  `100\%`,
		`a_b`:   `a\_b`,
		`a\b`:   `a\\b`,
		`plain`: `plain`,
	}
	for query, want := range cases {
		if got := escapeLikeQuery(query); got != want {
			t.Fatalf("escapeLikeQuery(%q) = %q, want %q", query, got, want)
		}
	}
}

func TestDecideWorkflowUpdateAction(t *testing.T) {
	t.Parallel()

	const (
		currentGeneration int64 = 3
		currentInterval   int32 = 5
		newInterval       int32 = 10
		buildHash               = "build-hash"
	)

	tests := []struct {
		name                           string
		currentBuildHash               sql.NullString
		currentBuildStatus             string
		reactivatingTerminatedWorkflow bool
		newBuildHash                   string
		newBuildHashValid              bool
		newInterval                    int32
		want                           updateWorkflowActionDecision
	}{
		{
			name: "terminated started workflow queues build",
			currentBuildHash: sql.NullString{
				String: buildHash,
				Valid:  true,
			},
			currentBuildStatus:             workflowsmodel.WorkflowBuildStatusStarted.ToString(),
			reactivatingTerminatedWorkflow: true,
			newBuildHash:                   buildHash,
			newBuildHashValid:              true,
			newInterval:                    currentInterval,
			want: updateWorkflowActionDecision{
				buildRequired:      true,
				rescheduleRequired: false,
				nextGeneration:     currentGeneration + 1,
				buildStatus:        workflowsmodel.WorkflowBuildStatusQueued.ToString(),
			},
		},
		{
			name: "terminated completed workflow queues build",
			currentBuildHash: sql.NullString{
				String: buildHash,
				Valid:  true,
			},
			currentBuildStatus:             workflowsmodel.WorkflowBuildStatusCompleted.ToString(),
			reactivatingTerminatedWorkflow: true,
			newBuildHash:                   buildHash,
			newBuildHashValid:              true,
			newInterval:                    currentInterval,
			want: updateWorkflowActionDecision{
				buildRequired:      true,
				rescheduleRequired: false,
				nextGeneration:     currentGeneration + 1,
				buildStatus:        workflowsmodel.WorkflowBuildStatusQueued.ToString(),
			},
		},
		{
			name: "active completed interval change reschedules",
			currentBuildHash: sql.NullString{
				String: buildHash,
				Valid:  true,
			},
			currentBuildStatus:             workflowsmodel.WorkflowBuildStatusCompleted.ToString(),
			reactivatingTerminatedWorkflow: false,
			newBuildHash:                   buildHash,
			newBuildHashValid:              true,
			newInterval:                    newInterval,
			want: updateWorkflowActionDecision{
				buildRequired:      false,
				rescheduleRequired: true,
				nextGeneration:     currentGeneration + 1,
			},
		},
		{
			name: "active started interval change waits for build completion",
			currentBuildHash: sql.NullString{
				String: buildHash,
				Valid:  true,
			},
			currentBuildStatus:             workflowsmodel.WorkflowBuildStatusStarted.ToString(),
			reactivatingTerminatedWorkflow: false,
			newBuildHash:                   buildHash,
			newBuildHashValid:              true,
			newInterval:                    newInterval,
			want: updateWorkflowActionDecision{
				buildRequired:      false,
				rescheduleRequired: false,
				nextGeneration:     currentGeneration,
			},
		},
		{
			name: "active build hash change queues build",
			currentBuildHash: sql.NullString{
				String: buildHash,
				Valid:  true,
			},
			currentBuildStatus:             workflowsmodel.WorkflowBuildStatusCompleted.ToString(),
			reactivatingTerminatedWorkflow: false,
			newBuildHash:                   "new-build-hash",
			newBuildHashValid:              true,
			newInterval:                    currentInterval,
			want: updateWorkflowActionDecision{
				buildRequired:      true,
				rescheduleRequired: false,
				nextGeneration:     currentGeneration + 1,
				buildStatus:        workflowsmodel.WorkflowBuildStatusQueued.ToString(),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := decideWorkflowUpdateAction(
				tt.currentBuildHash,
				currentGeneration,
				currentInterval,
				tt.currentBuildStatus,
				tt.reactivatingTerminatedWorkflow,
				tt.newBuildHash,
				tt.newBuildHashValid,
				tt.newInterval,
			); got != tt.want {
				t.Fatalf("decideWorkflowUpdateAction() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestValidateWorkflowPayloadKind(t *testing.T) {
	t.Parallel()

	const (
		heartbeatPayload = `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`
		containerPayload = `{"image": "alpine:latest", "cmd": ["echo", "hello world"]}`
	)

	tests := []struct {
		name    string
		kind    string
		payload string
		wantErr bool
	}{
		{
			name:    "heartbeat kind accepts heartbeat payload",
			kind:    workflowsmodel.KindHeartbeat.ToString(),
			payload: heartbeatPayload,
		},
		{
			name:    "container kind accepts container payload",
			kind:    workflowsmodel.KindContainer.ToString(),
			payload: containerPayload,
		},
		{
			name:    "heartbeat kind rejects container payload",
			kind:    workflowsmodel.KindHeartbeat.ToString(),
			payload: containerPayload,
			wantErr: true,
		},
		{
			name:    "container kind rejects heartbeat payload",
			kind:    workflowsmodel.KindContainer.ToString(),
			payload: heartbeatPayload,
			wantErr: true,
		},
		{
			name:    "unknown kind rejects any payload",
			kind:    "CRON",
			payload: heartbeatPayload,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := validateWorkflowPayloadKind(tt.kind, tt.payload); (err != nil) != tt.wantErr {
				t.Fatalf("validateWorkflowPayloadKind() err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestUpdateWorkflowBuildStatusRejectsMalformedUUIDsBeforeDatabase(t *testing.T) {
	t.Parallel()
	repo := New(&Config{FetchLimit: 20}, nil)
	for _, tc := range []struct {
		name, workflowID, userID string
	}{
		{name: "workflow", workflowID: "not-a-workflow", userID: "11111111-1111-4111-8111-111111111111"},
		{name: "user", workflowID: "550e8400-e29b-41d4-a716-446655440000", userID: "not-a-user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := repo.UpdateWorkflowBuildStatus(t.Context(), tc.workflowID, tc.userID, "STARTED", 1, "", "")
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Fatalf("UpdateWorkflowBuildStatus() code = %v, want %v (err: %v)", got, codes.InvalidArgument, err)
			}
		})
	}
}

func TestMapTerminalLookupError(t *testing.T) {
	t.Parallel()

	internalErr := mapTerminalLookupError(t.Context(), errors.New("lookup failed"))
	if got := status.Code(internalErr); got != codes.Internal {
		t.Fatalf("mapTerminalLookupError() code = %v, want %v (err: %v)", got, codes.Internal, internalErr)
	}
	if !strings.Contains(internalErr.Error(), "failed to check terminal workflow: lookup failed") {
		t.Fatalf("mapTerminalLookupError() message = %q", internalErr)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	canceledErr := mapTerminalLookupError(ctx, errors.New("lookup failed"))
	if got := status.Code(canceledErr); got != codes.Canceled {
		t.Fatalf("mapTerminalLookupError() code = %v, want %v (err: %v)", got, codes.Canceled, canceledErr)
	}
	if !strings.Contains(canceledErr.Error(), context.Canceled.Error()) {
		t.Fatalf("mapTerminalLookupError() message = %q", canceledErr)
	}
}
