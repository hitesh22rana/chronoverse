package notifications

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"
)

// Kind represents the kind of the notification.
type Kind string

// Kinds for the notification.
const (
	KindWebAlert   Kind = "WEB_ALERT"
	KindWebError   Kind = "WEB_ERROR"
	KindWebWarn    Kind = "WEB_WARN"
	KindWebSuccess Kind = "WEB_SUCCESS"
	KindWebInfo    Kind = "WEB_INFO"
)

// ToString converts the Kind to its string representation.
func (k Kind) ToString() string {
	return string(k)
}

// Entity represents the entity of the notification.
type Entity string

// Entities for the notification.
const (
	EntityWorkflow Entity = "WORKFLOW"
	EntityJob      Entity = "JOB"
)

// ToString converts the Entity to its string representation.
func (e Entity) ToString() string {
	return string(e)
}

// CreateWorkflowsNotificationPayload creates a notification payload for workflows.
func CreateWorkflowsNotificationPayload(title, message, workflowID string) (string, error) {
	//nolint:errcheck // All payload fields are strings.
	payload, _ := json.Marshal(map[string]string{
		"title":       title,
		"message":     message,
		"entity_id":   workflowID,
		"entity_type": EntityWorkflow.ToString(),
		"action_url":  fmt.Sprintf("/workflows/%s", workflowID),
	})

	return string(payload), nil
}

// CreateJobsNotificationPayload creates a notification payload for jobs.
func CreateJobsNotificationPayload(title, message, workflowID, jobID string) (string, error) {
	//nolint:errcheck // All payload fields are strings.
	payload, _ := json.Marshal(map[string]string{
		"title":       title,
		"message":     message,
		"entity_id":   jobID,
		"entity_type": EntityJob.ToString(),
		"action_url":  fmt.Sprintf("/workflows/%s/jobs/%s", workflowID, jobID),
	})

	return string(payload), nil
}

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
