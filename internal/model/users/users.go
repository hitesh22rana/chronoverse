package users

import (
	"time"

	userspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"
)

// GetUserResponse represents the response of GetUser.
type GetUserResponse struct {
	ID                     string    `db:"id"`
	Email                  string    `db:"email"`
	NotificationPreference string    `db:"notification_preference"`
	CreatedAt              time.Time `db:"created_at"`
	UpdatedAt              time.Time `db:"updated_at"`
}

// ToProto converts the GetUserResponse to its protobuf representation.
func (r *GetUserResponse) ToProto() *userspb.GetUserResponse {
	return &userspb.GetUserResponse{
		Id:                     r.ID,
		Email:                  r.Email,
		NotificationPreference: r.NotificationPreference,
		CreatedAt:              r.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:              r.UpdatedAt.Format(time.RFC3339Nano),
	}
}
