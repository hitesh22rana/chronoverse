package users

import (
	"time"

	userspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"
)

// LoginUserData represents the stored user data in the database.
// This is useful for the login process, where we need to check the password.
// This is only used in the repository layer to fetch the user data.
// In the service layer, UserResponse is returned, which is a subset of this data and used for caching.
type LoginUserData struct {
	ID                     string    `db:"id"`
	Email                  string    `db:"email"`
	Password               string    `db:"password"`
	NotificationPreference string    `db:"notification_preference"`
	CreatedAt              time.Time `db:"created_at"`
	UpdatedAt              time.Time `db:"updated_at"`
}

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
		Email:                  r.Email,
		NotificationPreference: r.NotificationPreference,
		CreatedAt:              r.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:              r.UpdatedAt.Format(time.RFC3339Nano),
	}
}
