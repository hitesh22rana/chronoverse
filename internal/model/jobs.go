package model

import (
	"database/sql"
	"time"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

// GetJobResponse represents the response of GetJob.
type GetJobResponse struct {
	ID           string       `db:"id"`
	Name         string       `db:"name"`
	Payload      string       `db:"payload"`
	Kind         string       `db:"kind"`
	Interval     int32        `db:"interval"`
	CreatedAt    time.Time    `db:"created_at"`
	UpdatedAt    time.Time    `db:"updated_at"`
	TerminatedAt sql.NullTime `db:"terminated_at,omitempty"`
}

// ToProto converts the GetJobResponse to its protobuf representation.
func (r *GetJobResponse) ToProto() *jobspb.GetJobResponse {
	var terminatedAt string
	if r.TerminatedAt.Valid {
		terminatedAt = r.TerminatedAt.Time.Format(time.RFC3339Nano)
	}

	return &jobspb.GetJobResponse{
		Id:           r.ID,
		Name:         r.Name,
		Payload:      r.Payload,
		Kind:         r.Kind,
		Interval:     r.Interval,
		CreatedAt:    r.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:    r.UpdatedAt.Format(time.RFC3339Nano),
		TerminatedAt: terminatedAt,
	}
}

// GetJobByIDResponse represents the response of GetJobByID.
type GetJobByIDResponse struct {
	ID           string       `db:"id"`
	UserID       string       `db:"user_id"`
	Name         string       `db:"name"`
	Payload      string       `db:"payload"`
	Kind         string       `db:"kind"`
	Interval     int32        `db:"interval"`
	CreatedAt    time.Time    `db:"created_at"`
	UpdatedAt    time.Time    `db:"updated_at"`
	TerminatedAt sql.NullTime `db:"terminated_at,omitempty"`
}

// ToProto converts the GetJobByIDResponse to its protobuf representation.
func (r *GetJobByIDResponse) ToProto() *jobspb.GetJobByIDResponse {
	var terminatedAt string
	if r.TerminatedAt.Valid {
		terminatedAt = r.TerminatedAt.Time.Format(time.RFC3339Nano)
	}

	return &jobspb.GetJobByIDResponse{
		Id:           r.ID,
		UserId:       r.UserID,
		Name:         r.Name,
		Payload:      r.Payload,
		Kind:         r.Kind,
		Interval:     r.Interval,
		CreatedAt:    r.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:    r.UpdatedAt.Format(time.RFC3339Nano),
		TerminatedAt: terminatedAt,
	}
}

// JobByUserIDResponse represents the response of ListJobsByUserID.
type JobByUserIDResponse struct {
	ID           string       `db:"id"`
	Name         string       `db:"name"`
	Payload      string       `db:"payload"`
	Kind         string       `db:"kind"`
	Interval     int32        `db:"interval"`
	CreatedAt    time.Time    `db:"created_at"`
	UpdatedAt    time.Time    `db:"updated_at"`
	TerminatedAt sql.NullTime `db:"terminated_at,omitempty"`
}

// ListJobsByUserIDResponse represents the response of ListJobsByUserID.
type ListJobsByUserIDResponse struct {
	Jobs   []*JobByUserIDResponse
	Cursor string
}

// ToProto converts the ListJobsByUserIDResponse to its protobuf representation.
func (r *ListJobsByUserIDResponse) ToProto() *jobspb.ListJobsByUserIDResponse {
	jobs := make([]*jobspb.JobsByUserIDResponse, len(r.Jobs))
	for i := range r.Jobs {
		j := r.Jobs[i]

		var terminatedAt string
		if j.TerminatedAt.Valid {
			terminatedAt = j.TerminatedAt.Time.Format(time.RFC3339Nano)
		}

		jobs[i] = &jobspb.JobsByUserIDResponse{
			Id:           j.ID,
			Name:         j.Name,
			Payload:      j.Payload,
			Kind:         j.Kind,
			Interval:     j.Interval,
			CreatedAt:    j.CreatedAt.Format(time.RFC3339Nano),
			UpdatedAt:    j.UpdatedAt.Format(time.RFC3339Nano),
			TerminatedAt: terminatedAt,
		}
	}

	return &jobspb.ListJobsByUserIDResponse{
		Jobs:   jobs,
		Cursor: r.Cursor,
	}
}

// ScheduledJobByJobIDResponse represents the response of ListScheduledJobsByID.
type ScheduledJobByJobIDResponse struct {
	ID          string       `db:"id"`
	Status      string       `db:"status"`
	ScheduledAt time.Time    `db:"scheduled_at"`
	RetryCount  int32        `db:"retry_count"`
	MaxRetry    int32        `db:"max_retry"`
	StartedAt   sql.NullTime `db:"started_at,omitempty"`
	CompletedAt sql.NullTime `db:"completed_at,omitempty"`
	CreatedAt   time.Time    `db:"created_at"`
	UpdatedAt   time.Time    `db:"updated_at"`
}

// ListScheduledJobsResponse represents the response of ListScheduledJobsByID.
type ListScheduledJobsResponse struct {
	ScheduledJobs []*ScheduledJobByJobIDResponse
	Cursor        string
}

// ToProto converts the ListScheduledJobsResponse to its protobuf representation.
func (r *ListScheduledJobsResponse) ToProto() *jobspb.ListScheduledJobsResponse {
	scheduledJobs := make([]*jobspb.ScheduledJobsResponse, len(r.ScheduledJobs))
	for i := range r.ScheduledJobs {
		j := r.ScheduledJobs[i]

		var startedAt, completedAt string
		if j.StartedAt.Valid {
			startedAt = j.StartedAt.Time.Format(time.RFC3339Nano)
		}
		if j.CompletedAt.Valid {
			completedAt = j.CompletedAt.Time.Format(time.RFC3339Nano)
		}

		scheduledJobs[i] = &jobspb.ScheduledJobsResponse{
			Id:          j.ID,
			Status:      j.Status,
			ScheduledAt: j.ScheduledAt.Format(time.RFC3339Nano),
			RetryCount:  j.RetryCount,
			MaxRetry:    j.MaxRetry,
			StartedAt:   startedAt,
			CompletedAt: completedAt,
			CreatedAt:   j.CreatedAt.Format(time.RFC3339Nano),
			UpdatedAt:   j.UpdatedAt.Format(time.RFC3339Nano),
		}
	}

	return &jobspb.ListScheduledJobsResponse{
		ScheduledJobs: scheduledJobs,
		Cursor:        r.Cursor,
	}
}
