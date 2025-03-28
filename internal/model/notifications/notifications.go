package notifications

import (
	"database/sql"
	"time"

	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"
)

// NotificationResponse represents a notification entity.
type NotificationResponse struct {
	ID        string       `db:"id"`
	Kind      string       `db:"kind"`
	Payload   string       `db:"payload"`
	ReadAt    sql.NullTime `db:"read_at"`
	CreatedAt time.Time    `db:"created_at"`
	UpdatedAt time.Time    `db:"updated_at"`
}

// ListNotificationsResponse represents the result of ListNotifications.
type ListNotificationsResponse struct {
	Notifications []*NotificationResponse
	Cursor        string
}

// ToProto converts the ListNotificationsResponse to its protobuf representation.
func (r *ListNotificationsResponse) ToProto() *notificationspb.ListNotificationsResponse {
	notifications := make([]*notificationspb.NotificationResponse, 0, len(r.Notifications))
	for _, notification := range r.Notifications {
		var readAt string
		if notification.ReadAt.Valid {
			readAt = notification.ReadAt.Time.Format(time.RFC3339Nano)
		}

		notifications = append(notifications, &notificationspb.NotificationResponse{
			Id:        notification.ID,
			Kind:      notification.Kind,
			Payload:   notification.Payload,
			ReadAt:    readAt,
			CreatedAt: notification.CreatedAt.Format(time.RFC3339Nano),
			UpdatedAt: notification.UpdatedAt.Format(time.RFC3339Nano),
		})
	}

	return &notificationspb.ListNotificationsResponse{
		Notifications: notifications,
		Cursor:        r.Cursor,
	}
}
