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

// GetJobResponse represents the response of GetJob.
type GetJobResponse struct {
	ID          string       `db:"id"`
	WorkflowID  string       `db:"workflow_id"`
	JobStatus   string       `db:"status"`
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
		Status:      r.JobStatus,
		ScheduledAt: r.ScheduledAt.Format(time.RFC3339Nano),
		StartedAt:   startedAt,
		CompletedAt: completedAt,
		CreatedAt:   r.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:   r.UpdatedAt.Format(time.RFC3339Nano),
	}
}

// ScheduledJobEntry represents the scheduled job entry.
type ScheduledJobEntry struct {
	JobID       string
	WorkflowID  string
	ScheduledAt string
}

// JobLog represents the log of the job.
type JobLog struct {
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
	}
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

// JobByWorkflowIDResponse represents the response of ListJobsByID.
type JobByWorkflowIDResponse struct {
	ID          string         `db:"id"`
	WorkflowID  string         `db:"workflow_id"`
	ContainerID sql.NullString `db:"container_id,omitempty"` // Unique identifier for the container, if applicable
	JobStatus   string         `db:"status"`
	ScheduledAt time.Time      `db:"scheduled_at"`
	StartedAt   sql.NullTime   `db:"started_at,omitempty"`
	CompletedAt sql.NullTime   `db:"completed_at,omitempty"`
	CreatedAt   time.Time      `db:"created_at"`
	UpdatedAt   time.Time      `db:"updated_at"`
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
			ScheduledAt: j.ScheduledAt.Format(time.RFC3339Nano),
			StartedAt:   startedAt,
			CompletedAt: completedAt,
			CreatedAt:   j.CreatedAt.Format(time.RFC3339Nano),
			UpdatedAt:   j.UpdatedAt.Format(time.RFC3339Nano),
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

// ListJobsFilters represents the filters for listing jobs.
type ListJobsFilters struct {
	Status string `validate:"omitempty"`
}

// SearchJobLogsFilters represents the filters for searching jobs.
type SearchJobLogsFilters struct {
	Stream  int    `validate:"required,min=1,max=3"`
	Message string `validate:"required"`
}
