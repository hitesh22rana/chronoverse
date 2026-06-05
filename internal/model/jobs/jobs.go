package jobs

import (
	"database/sql"
	"time"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

// JobStatus represents the status of the scheduled job.
type JobStatus string

// JobStatuses for the scheduled job.
const (
	JobStatusPending   JobStatus = "PENDING"
	JobStatusQueued    JobStatus = "QUEUED"
	JobStatusRunning   JobStatus = "RUNNING"
	JobStatusCompleted JobStatus = "COMPLETED"
	JobStatusFailed    JobStatus = "FAILED"
	JobStatusCanceled  JobStatus = "CANCELED"
)

// ToString converts the JobStatus to its string representation.
func (s JobStatus) ToString() string {
	return string(s)
}

// FailureKind represents whether a job failure belongs to user code/configuration or system infrastructure.
type FailureKind string

// FailureKinds for terminal job failures.
const (
	FailureKindUser   FailureKind = "USER"
	FailureKindSystem FailureKind = "SYSTEM"
)

// ToString converts the FailureKind to its string representation.
func (f FailureKind) ToString() string {
	return string(f)
}

// JobTrigger represents the trigger type of the job.
type JobTrigger string

// JobTriggers for the scheduled job.
const (
	JobTriggerAutomatic JobTrigger = "AUTOMATIC"
	JobTriggerManual    JobTrigger = "MANUAL"
)

// ToString converts the JobTrigger to its string representation.
func (t JobTrigger) ToString() string {
	return string(t)
}

// GetJobResponse represents the response of GetJob.
type GetJobResponse struct {
	ID          string       `db:"id"`
	WorkflowID  string       `db:"workflow_id"`
	JobStatus   string       `db:"status"`
	JobTrigger  string       `db:"trigger"`
	ScheduledAt time.Time    `db:"scheduled_at"`
	StartedAt   sql.NullTime `db:"started_at,omitempty"`
	CompletedAt sql.NullTime `db:"completed_at,omitempty"`
	CreatedAt   time.Time    `db:"created_at"`
	UpdatedAt   time.Time    `db:"updated_at"`
}

// ToProto converts the GetJobResponse to its protobuf representation.
func (r *GetJobResponse) ToProto() *jobspb.GetJobResponse {
	var startedAt, completedAt string
	if r.StartedAt.Valid {
		startedAt = r.StartedAt.Time.Format(time.RFC3339Nano)
	}
	if r.CompletedAt.Valid {
		completedAt = r.CompletedAt.Time.Format(time.RFC3339Nano)
	}

	return &jobspb.GetJobResponse{
		Id:          r.ID,
		WorkflowId:  r.WorkflowID,
		Status:      r.JobStatus,
		Trigger:     r.JobTrigger,
		ScheduledAt: r.ScheduledAt.Format(time.RFC3339Nano),
		StartedAt:   startedAt,
		CompletedAt: completedAt,
		CreatedAt:   r.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:   r.UpdatedAt.Format(time.RFC3339Nano),
	}
}

// GetJobByIDResponse represents the response of GetJobByID.
type GetJobByIDResponse struct {
	ID          string         `db:"id"`
	WorkflowID  string         `db:"workflow_id"`
	ContainerID sql.NullString `db:"container_id,omitempty"` // Unique identifier for the container, if applicable
	UserID      string         `db:"user_id"`
	JobStatus   string         `db:"status"`
	JobTrigger  string         `db:"trigger"`
	Attempts    int32          `db:"attempts"`
	ScheduledAt time.Time      `db:"scheduled_at"`
	StartedAt   sql.NullTime   `db:"started_at,omitempty"`
	CompletedAt sql.NullTime   `db:"completed_at,omitempty"`
	CreatedAt   time.Time      `db:"created_at"`
	UpdatedAt   time.Time      `db:"updated_at"`
}

// ToProto converts the GetJobByIDResponse to its protobuf representation.
func (r *GetJobByIDResponse) ToProto() *jobspb.GetJobByIDResponse {
	var startedAt, completedAt string
	if r.StartedAt.Valid {
		startedAt = r.StartedAt.Time.Format(time.RFC3339Nano)
	}
	if r.CompletedAt.Valid {
		completedAt = r.CompletedAt.Time.Format(time.RFC3339Nano)
	}

	return &jobspb.GetJobByIDResponse{
		Id:          r.ID,
		WorkflowId:  r.WorkflowID,
		UserId:      r.UserID,
		ContainerId: r.ContainerID.String,
		Status:      r.JobStatus,
		Trigger:     r.JobTrigger,
		ScheduledAt: r.ScheduledAt.Format(time.RFC3339Nano),
		StartedAt:   startedAt,
		CompletedAt: completedAt,
		CreatedAt:   r.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:   r.UpdatedAt.Format(time.RFC3339Nano),
	}
}

// ScheduledJobEntry represents the scheduled job entry.
type ScheduledJobEntry struct {
	EventKey           string
	JobID              string
	WorkflowID         string
	ScheduledAt        string
	DispatchAttempt    int32
	WorkflowGeneration int64
}

// ClaimedJob represents a job claimed by an execution worker.
type ClaimedJob struct {
	ID               string    `db:"id"`
	WorkflowID       string    `db:"workflow_id"`
	UserID           string    `db:"user_id"`
	Trigger          string    `db:"trigger"`
	ScheduledAt      time.Time `db:"scheduled_at"`
	DispatchAttempts int32     `db:"dispatch_attempts"`
	Attempts         int32     `db:"attempts"`
	LeaseToken       string    `db:"lease_token"`
}

// ToClaimJobProto converts a ClaimedJob to a ClaimJobResponse.
func (j *ClaimedJob) ToClaimJobProto(claimed bool, reason string) *jobspb.ClaimJobResponse {
	if j == nil {
		return &jobspb.ClaimJobResponse{
			Claimed: claimed,
			Reason:  reason,
		}
	}

	return &jobspb.ClaimJobResponse{
		Claimed:          claimed,
		Reason:           reason,
		Id:               j.ID,
		WorkflowId:       j.WorkflowID,
		UserId:           j.UserID,
		Trigger:          j.Trigger,
		ScheduledAt:      j.ScheduledAt.Format(time.RFC3339Nano),
		DispatchAttempts: j.DispatchAttempts,
		Attempts:         j.Attempts,
		LeaseToken:       j.LeaseToken,
	}
}

// ExpiredJobLease represents a running job with an expired lease.
type ExpiredJobLease struct {
	ID           string         `db:"id"`
	WorkflowID   string         `db:"workflow_id"`
	UserID       string         `db:"user_id"`
	ContainerID  sql.NullString `db:"container_id,omitempty"`
	LeaseToken   string         `db:"lease_token"`
	LeasedBy     sql.NullString `db:"leased_by,omitempty"`
	Trigger      string         `db:"trigger"`
	ScheduledAt  time.Time      `db:"scheduled_at"`
	Attempts     int32          `db:"attempts"`
	LogRetention bool           `db:"log_retention"`
}

// ToProto converts an ExpiredJobLease to its protobuf representation.
func (j *ExpiredJobLease) ToProto() *jobspb.ExpiredJobLease {
	return &jobspb.ExpiredJobLease{
		Id:           j.ID,
		WorkflowId:   j.WorkflowID,
		UserId:       j.UserID,
		ContainerId:  j.ContainerID.String,
		LeaseToken:   j.LeaseToken,
		LeasedBy:     j.LeasedBy.String,
		Trigger:      j.Trigger,
		ScheduledAt:  j.ScheduledAt.Format(time.RFC3339Nano),
		Attempts:     j.Attempts,
		LogRetention: j.LogRetention,
	}
}

// JobLog represents the log of the job.
type JobLog struct {
	EventID     string    `db:"event_id"`
	Timestamp   time.Time `db:"timestamp"`
	Message     string    `db:"message"`
	SequenceNum uint32    `db:"sequence_num"`
	Stream      string    `db:"stream"` // "stdout" or "stderr"
}

// ToProto converts the JobLog to its protobuf representation.
func (l *JobLog) ToProto() *jobspb.Log {
	return &jobspb.Log{
		Timestamp:   l.Timestamp.Format(time.RFC3339Nano),
		Message:     l.Message,
		SequenceNum: l.SequenceNum,
		Stream:      l.Stream,
		EventId:     l.EventID,
	}
}

// JobLogsSortOrder represents retained job log ordering.
type JobLogsSortOrder int

// Job log sort orders.
const (
	JobLogsSortOrderUnspecified JobLogsSortOrder = 0
	JobLogsSortOrderDesc        JobLogsSortOrder = 1
	JobLogsSortOrderAsc         JobLogsSortOrder = 2
)

// GetJobLogsFilters represents the filters for filtering job logs.
type GetJobLogsFilters struct {
	Stream int `validate:"required,min=1,max=3"`
}

// GetJobLogsResponse represents the response of GetJobLogs.
type GetJobLogsResponse struct {
	ID         string
	WorkflowID string
	JobLogs    []*JobLog
	Cursor     string
}

// ToProto converts the GetJobLogsResponse to its protobuf representation.
func (r *GetJobLogsResponse) ToProto() *jobspb.GetJobLogsResponse {
	jobLogs := make([]*jobspb.Log, len(r.JobLogs))
	for i := range r.JobLogs {
		l := r.JobLogs[i]
		jobLogs[i] = l.ToProto()
	}

	return &jobspb.GetJobLogsResponse{
		Id:         r.ID,
		WorkflowId: r.WorkflowID,
		Logs:       jobLogs,
		Cursor:     r.Cursor,
	}
}

// SearchJobLogsFilters represents the filters for searching job logs.
type SearchJobLogsFilters struct {
	Stream  int    `validate:"required,min=1,max=3"`
	Message string `validate:"required"`
}

// JobByWorkflowIDResponse represents the response of ListJobsByID.
type JobByWorkflowIDResponse struct {
	ID          string         `db:"id"`
	WorkflowID  string         `db:"workflow_id"`
	ContainerID sql.NullString `db:"container_id,omitempty"` // Unique identifier for the container, if applicable
	JobStatus   string         `db:"status"`
	JobTrigger  string         `db:"trigger"`
	Attempts    int32          `db:"attempts"`
	ScheduledAt time.Time      `db:"scheduled_at"`
	StartedAt   sql.NullTime   `db:"started_at,omitempty"`
	CompletedAt sql.NullTime   `db:"completed_at,omitempty"`
	CreatedAt   time.Time      `db:"created_at"`
	UpdatedAt   time.Time      `db:"updated_at"`
}

// ListJobsFilters represents the filters for listing jobs.
type ListJobsFilters struct {
	Status  string `validate:"omitempty"`
	Trigger string `validate:"omitempty"`
}

// ListJobsResponse represents the response of ListJobsByID.
type ListJobsResponse struct {
	Jobs   []*JobByWorkflowIDResponse
	Cursor string
}

// ToProto converts the ListJobsResponse to its protobuf representation.
// It takes an internalService boolean to determine if the some fields.
func (r *ListJobsResponse) ToProto(internalService bool) *jobspb.ListJobsResponse {
	jobs := make([]*jobspb.JobsResponse, len(r.Jobs))
	for i := range r.Jobs {
		j := r.Jobs[i]

		var startedAt, completedAt string
		if j.StartedAt.Valid {
			startedAt = j.StartedAt.Time.Format(time.RFC3339Nano)
		}
		if j.CompletedAt.Valid {
			completedAt = j.CompletedAt.Time.Format(time.RFC3339Nano)
		}

		jobs[i] = &jobspb.JobsResponse{
			Id:          j.ID,
			WorkflowId:  j.WorkflowID,
			Status:      j.JobStatus,
			Trigger:     j.JobTrigger,
			ScheduledAt: j.ScheduledAt.Format(time.RFC3339Nano),
			StartedAt:   startedAt,
			CompletedAt: completedAt,
			CreatedAt:   j.CreatedAt.Format(time.RFC3339Nano),
			UpdatedAt:   j.UpdatedAt.Format(time.RFC3339Nano),
			Attempts:    j.Attempts,
		}

		if internalService {
			jobs[i].ContainerId = j.ContainerID.String
		}
	}

	return &jobspb.ListJobsResponse{
		Jobs:   jobs,
		Cursor: r.Cursor,
	}
}
