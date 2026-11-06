package notifications_test

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	model "github.com/hitesh22rana/chronoverse/internal/model/notifications"
	pb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"
)

func TestNotificationPayloadsPreserveTextAndEntityRoutes(t *testing.T) {
	t.Parallel()
	const title = "Alert <tag> & \"quoted\""
	const message = "first line\nsecond line \\ path"
	for _, job := range []bool{false, true} {
		payload, err := model.CreateWorkflowsNotificationPayload(title, message, "workflow")
		entity, entityID, url := "WORKFLOW", "workflow", "/workflows/workflow"
		if job {
			payload, err = model.CreateJobsNotificationPayload(title, message, "workflow", "job")
			entity, entityID, url = "JOB", "job", "/workflows/workflow/jobs/job"
		}
		require.NoError(t, err)
		var decoded map[string]string
		require.NoError(t, json.Unmarshal([]byte(payload), &decoded))
		require.Equal(t, map[string]string{"title": title, "message": message, "entity_id": entityID, "entity_type": entity, "action_url": url}, decoded)
	}
}

func TestReadAndUnreadNotificationsPreserveProtocolFields(t *testing.T) {
	t.Parallel()
	timestamp := time.Date(2026, time.November, 6, 1, 2, 3, 123, time.UTC)
	response := (&model.ListNotificationsResponse{Cursor: "next", Notifications: []*model.NotificationResponse{
		{ID: "read", Kind: model.KindWebWarn.ToString(), Payload: `{"title":"read"}`, ReadAt: sql.NullTime{Time: timestamp, Valid: true}, CreatedAt: timestamp, UpdatedAt: timestamp},
		{ID: "unread", Kind: "WEB_INFO", Payload: `{"title":"unread"}`, CreatedAt: timestamp, UpdatedAt: timestamp},
	}}).ToProto()
	want := &pb.ListNotificationsResponse{Cursor: "next", Notifications: []*pb.NotificationResponse{
		{Id: "read", Kind: "WEB_WARN", Payload: `{"title":"read"}`, ReadAt: "2026-11-06T01:02:03.000000123Z", CreatedAt: "2026-11-06T01:02:03.000000123Z", UpdatedAt: "2026-11-06T01:02:03.000000123Z"},
		{Id: "unread", Kind: "WEB_INFO", Payload: `{"title":"unread"}`, CreatedAt: "2026-11-06T01:02:03.000000123Z", UpdatedAt: "2026-11-06T01:02:03.000000123Z"},
	}}
	require.True(t, proto.Equal(want, response))
}
