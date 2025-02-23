package model

import (
	"database/sql"
	"time"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

// GetJobByIDResponse represents the response of GetJobByID.
type GetJobByIDResponse struct {
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
		terminatedAt = r.TerminatedAt.Time.Format(time.RFC3339)
	}

	return &jobspb.GetJobByIDResponse{
		UserId:       r.UserID,
		Name:         r.Name,
		Payload:      r.Payload,
		Kind:         r.Kind,
		Interval:     r.Interval,
		CreatedAt:    r.CreatedAt.Format(time.RFC3339),
		UpdatedAt:    r.UpdatedAt.Format(time.RFC3339),
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
	Jobs []*JobByUserIDResponse
}

// ToProto converts the ListJobsByUserIDResponse to its protobuf representation.
func (r *ListJobsByUserIDResponse) ToProto() *jobspb.ListJobsByUserIDResponse {
	jobs := make([]*jobspb.JobsByUserIDResponse, len(r.Jobs))
	for i := 0; i < len(r.Jobs); i++ {
		j := r.Jobs[i]

		var terminatedAt string
		if j.TerminatedAt.Valid {
			terminatedAt = j.TerminatedAt.Time.Format(time.RFC3339)
		}

		jobs[i] = &jobspb.JobsByUserIDResponse{
			Id:           j.ID,
			Name:         j.Name,
			Payload:      j.Payload,
			Kind:         j.Kind,
			Interval:     j.Interval,
			CreatedAt:    j.CreatedAt.Format(time.RFC3339),
			UpdatedAt:    j.UpdatedAt.Format(time.RFC3339),
			TerminatedAt: terminatedAt,
		}
	}

	return &jobspb.ListJobsByUserIDResponse{Jobs: jobs}
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

// ListScheduledJobsByJobIDResponse represents the response of ListScheduledJobsByID.
type ListScheduledJobsByJobIDResponse struct {
	ScheduledJobs []*ScheduledJobByJobIDResponse
}

// ToProto converts the ListScheduledJobsByJobIDResponse to its protobuf representation.
func (r *ListScheduledJobsByJobIDResponse) ToProto() *jobspb.ListScheduledJobsByJobIDResponse {
	scheduledJobs := make([]*jobspb.ScheduledJobsByIDResponse, len(r.ScheduledJobs))
	for i := 0; i < len(r.ScheduledJobs); i++ {
		j := r.ScheduledJobs[i]

		var startedAt, completedAt string
		if j.StartedAt.Valid {
			startedAt = j.StartedAt.Time.Format(time.RFC3339)
		}
		if j.CompletedAt.Valid {
			completedAt = j.CompletedAt.Time.Format(time.RFC3339)
		}

		scheduledJobs[i] = &jobspb.ScheduledJobsByIDResponse{
			Id:          j.ID,
			Status:      j.Status,
			ScheduledAt: j.ScheduledAt.Format(time.RFC3339),
			RetryCount:  j.RetryCount,
			MaxRetry:    j.MaxRetry,
			StartedAt:   startedAt,
			CompletedAt: completedAt,
			CreatedAt:   j.CreatedAt.Format(time.RFC3339),
			UpdatedAt:   j.UpdatedAt.Format(time.RFC3339),
		}
	}

	return &jobspb.ListScheduledJobsByJobIDResponse{ScheduledJobs: scheduledJobs}
}
