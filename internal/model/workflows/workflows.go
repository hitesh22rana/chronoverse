package workflows

import (
	"database/sql"
	"time"

	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
)

// Kind represents the kind of the job.
type Kind string

// Kinds for the job.
const (
	KindHeartbeat Kind = "HEARTBEAT"
	KindContainer Kind = "CONTAINER"
)

// ToString converts the Kind to its string representation.
func (k Kind) ToString() string {
	return string(k)
}

// WorkflowBuildStatus represents the status of the job build.
type WorkflowBuildStatus string

// WorkflowBuildStatuses for the job build.
const (
	WorkflowBuildStatusQueued    WorkflowBuildStatus = "QUEUED"
	WorkflowBuildStatusStarted   WorkflowBuildStatus = "STARTED"
	WorkflowBuildStatusCompleted WorkflowBuildStatus = "COMPLETED"
	WorkflowBuildStatusFailed    WorkflowBuildStatus = "FAILED"
	WorkflowBuildStatusCanceled  WorkflowBuildStatus = "CANCELED"
)

// ToString converts the WorkflowBuildStatus to its string representation.
func (j WorkflowBuildStatus) ToString() string {
	return string(j)
}

// GetWorkflowResponse represents the response of GetWorkflow.
type GetWorkflowResponse struct {
	ID                               string       `db:"id"`
	Name                             string       `db:"name"`
	Payload                          string       `db:"payload"`
	Kind                             string       `db:"kind"`
	WorkflowBuildStatus              string       `db:"build_status"`
	Interval                         int32        `db:"interval"`
	ConsecutiveJobFailuresCount      int32        `db:"consecutive_job_failures_count"`
	MaxConsecutiveJobFailuresAllowed int32        `db:"max_consecutive_job_failures_allowed"`
	CreatedAt                        time.Time    `db:"created_at"`
	UpdatedAt                        time.Time    `db:"updated_at"`
	TerminatedAt                     sql.NullTime `db:"terminated_at,omitempty"`
}

// ToProto converts the GetWorkflowResponse to its protobuf representation.
func (r *GetWorkflowResponse) ToProto() *workflowspb.GetWorkflowResponse {
	var terminatedAt string
	if r.TerminatedAt.Valid {
		terminatedAt = r.TerminatedAt.Time.Format(time.RFC3339Nano)
	}

	return &workflowspb.GetWorkflowResponse{
		Id:                               r.ID,
		Name:                             r.Name,
		Payload:                          r.Payload,
		Kind:                             r.Kind,
		BuildStatus:                      r.WorkflowBuildStatus,
		Interval:                         r.Interval,
		ConsecutiveJobFailuresCount:      r.ConsecutiveJobFailuresCount,
		MaxConsecutiveJobFailuresAllowed: r.MaxConsecutiveJobFailuresAllowed,
		CreatedAt:                        r.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:                        r.UpdatedAt.Format(time.RFC3339Nano),
		TerminatedAt:                     terminatedAt,
	}
}

// GetWorkflowByIDResponse represents the response of GetWorkflowByID.
type GetWorkflowByIDResponse struct {
	ID                               string       `db:"id"`
	UserID                           string       `db:"user_id"`
	Name                             string       `db:"name"`
	Payload                          string       `db:"payload"`
	Kind                             string       `db:"kind"`
	WorkflowBuildStatus              string       `db:"build_status"`
	Interval                         int32        `db:"interval"`
	ConsecutiveJobFailuresCount      int32        `db:"consecutive_job_failures_count"`
	MaxConsecutiveJobFailuresAllowed int32        `db:"max_consecutive_job_failures_allowed"`
	CreatedAt                        time.Time    `db:"created_at"`
	UpdatedAt                        time.Time    `db:"updated_at"`
	TerminatedAt                     sql.NullTime `db:"terminated_at,omitempty"`
}

// ToProto converts the GetWorkflowByIDResponse to its protobuf representation.
func (r *GetWorkflowByIDResponse) ToProto() *workflowspb.GetWorkflowByIDResponse {
	var terminatedAt string
	if r.TerminatedAt.Valid {
		terminatedAt = r.TerminatedAt.Time.Format(time.RFC3339Nano)
	}

	return &workflowspb.GetWorkflowByIDResponse{
		Id:                               r.ID,
		UserId:                           r.UserID,
		Name:                             r.Name,
		Payload:                          r.Payload,
		Kind:                             r.Kind,
		BuildStatus:                      r.WorkflowBuildStatus,
		Interval:                         r.Interval,
		ConsecutiveJobFailuresCount:      r.ConsecutiveJobFailuresCount,
		MaxConsecutiveJobFailuresAllowed: r.MaxConsecutiveJobFailuresAllowed,
		CreatedAt:                        r.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:                        r.UpdatedAt.Format(time.RFC3339Nano),
		TerminatedAt:                     terminatedAt,
	}
}

// WorkflowByUserIDResponse represents the response of ListWorkflowsByUserID.
type WorkflowByUserIDResponse struct {
	ID                               string       `db:"id"`
	Name                             string       `db:"name"`
	Payload                          string       `db:"payload"`
	Kind                             string       `db:"kind"`
	WorkflowBuildStatus              string       `db:"build_status"`
	Interval                         int32        `db:"interval"`
	ConsecutiveJobFailuresCount      int32        `db:"consecutive_job_failures_count"`
	MaxConsecutiveJobFailuresAllowed int32        `db:"max_consecutive_job_failures_allowed"`
	CreatedAt                        time.Time    `db:"created_at"`
	UpdatedAt                        time.Time    `db:"updated_at"`
	TerminatedAt                     sql.NullTime `db:"terminated_at,omitempty"`
}

// ListWorkflowsResponse represents the response of ListWorkflowsByUserID.
type ListWorkflowsResponse struct {
	Workflows []*WorkflowByUserIDResponse
	Cursor    string
}

// ToProto converts the ListWorkflowsResponse to its protobuf representation.
func (r *ListWorkflowsResponse) ToProto() *workflowspb.ListWorkflowsResponse {
	jobs := make([]*workflowspb.WorkflowsByUserIDResponse, len(r.Workflows))
	for i := range r.Workflows {
		j := r.Workflows[i]

		var terminatedAt string
		if j.TerminatedAt.Valid {
			terminatedAt = j.TerminatedAt.Time.Format(time.RFC3339Nano)
		}

		jobs[i] = &workflowspb.WorkflowsByUserIDResponse{
			Id:                               j.ID,
			Name:                             j.Name,
			Payload:                          j.Payload,
			Kind:                             j.Kind,
			BuildStatus:                      j.WorkflowBuildStatus,
			Interval:                         j.Interval,
			ConsecutiveJobFailuresCount:      j.ConsecutiveJobFailuresCount,
			MaxConsecutiveJobFailuresAllowed: j.MaxConsecutiveJobFailuresAllowed,
			CreatedAt:                        j.CreatedAt.Format(time.RFC3339Nano),
			UpdatedAt:                        j.UpdatedAt.Format(time.RFC3339Nano),
			TerminatedAt:                     terminatedAt,
		}
	}

	return &workflowspb.ListWorkflowsResponse{
		Workflows: jobs,
		Cursor:    r.Cursor,
	}
}

// ListWorkflowsFilters represents the filters for listing workflows.
type ListWorkflowsFilters struct {
	Query        string `validate:"omitempty"`
	Kind         string `validate:"omitempty"`
	BuildStatus  string `validate:"omitempty"`
	IsTerminated bool   `validate:"omitempty"`
	IntervalMin  int32  `validate:"omitempty"`
	IntervalMax  int32  `validate:"omitempty"`
}

// TestWorkflowRunStatus represents the status of a test workflow run.
type TestWorkflowRunStatus int64

// TestWorkflowRunStatus represents the status of a test workflow run.
const (
	TestWorkflowRunStatusUnspecified TestWorkflowRunStatus = 0
	TestWorkflowRunStatusBuilding    TestWorkflowRunStatus = 1
	TestWorkflowRunStatusRunning     TestWorkflowRunStatus = 2
	TestWorkflowRunStatusCompleted   TestWorkflowRunStatus = 3
	TestWorkflowRunStatusFailed      TestWorkflowRunStatus = 4
	TestWorkflowRunStatusCanceled    TestWorkflowRunStatus = 5
)

// StreamTestWorkflowRunResponse represents the response of StreamTestWorkflowRun.
type StreamTestWorkflowRunResponse struct {
	Status  TestWorkflowRunStatus
	Kind    Kind
	Log     *jobsmodel.JobLog
	Message string
}

// ToProto converts the StreamTestWorkflowRunResponse to its protobuf representation.
//
//nolint:exhaustive // Skip un-necessary switch cases
func (r *StreamTestWorkflowRunResponse) ToProto() *workflowspb.StreamTestWorkflowRunResponse {
	switch r.Status {
	case TestWorkflowRunStatusUnspecified:
		return &workflowspb.StreamTestWorkflowRunResponse{
			Status: workflowspb.TestWorkflowRunStatus_TEST_WORKFLOW_RUN_STATUS_UNSPECIFIED,
			Data:   nil,
		}
	case TestWorkflowRunStatusBuilding:
		return &workflowspb.StreamTestWorkflowRunResponse{
			Status: workflowspb.TestWorkflowRunStatus_TEST_WORKFLOW_RUN_STATUS_BUILDING,
			Data: &workflowspb.StreamTestWorkflowRunResponse_Message{
				Message: r.Message,
			},
		}
	case TestWorkflowRunStatusRunning:
		switch r.Kind {
		case KindContainer:
			return &workflowspb.StreamTestWorkflowRunResponse{
				Status: workflowspb.TestWorkflowRunStatus_TEST_WORKFLOW_RUN_STATUS_RUNNING,
				Data: &workflowspb.StreamTestWorkflowRunResponse_Log{
					Log: r.Log.ToProto(),
				},
			}
		default:
			return &workflowspb.StreamTestWorkflowRunResponse{
				Status: workflowspb.TestWorkflowRunStatus_TEST_WORKFLOW_RUN_STATUS_RUNNING,
				Data: &workflowspb.StreamTestWorkflowRunResponse_Message{
					Message: r.Message,
				},
			}
		}
	case TestWorkflowRunStatusCompleted:
		return &workflowspb.StreamTestWorkflowRunResponse{
			Status: workflowspb.TestWorkflowRunStatus_TEST_WORKFLOW_RUN_STATUS_COMPLETED,
			Data: &workflowspb.StreamTestWorkflowRunResponse_Message{
				Message: r.Message,
			},
		}
	case TestWorkflowRunStatusFailed:
		return &workflowspb.StreamTestWorkflowRunResponse{
			Status: workflowspb.TestWorkflowRunStatus_TEST_WORKFLOW_RUN_STATUS_FAILED,
			Data: &workflowspb.StreamTestWorkflowRunResponse_Message{
				Message: r.Message,
			},
		}
	case TestWorkflowRunStatusCanceled:
		return &workflowspb.StreamTestWorkflowRunResponse{
			Status: workflowspb.TestWorkflowRunStatus_TEST_WORKFLOW_RUN_STATUS_CANCELED,
			Data: &workflowspb.StreamTestWorkflowRunResponse_Message{
				Message: r.Message,
			},
		}
	default:
		return &workflowspb.StreamTestWorkflowRunResponse{
			Status: workflowspb.TestWorkflowRunStatus_TEST_WORKFLOW_RUN_STATUS_UNSPECIFIED,
			Data:   nil,
		}
	}
}
