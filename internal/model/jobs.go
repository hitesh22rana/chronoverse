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
