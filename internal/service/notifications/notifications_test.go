package notifications_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	notificationsmodel "github.com/hitesh22rana/chronoverse/internal/model/notifications"
	"github.com/hitesh22rana/chronoverse/internal/service/notifications"
	notificationsmock "github.com/hitesh22rana/chronoverse/internal/service/notifications/mock"
	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"
)

func TestService_CreateNotification(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := notificationsmock.NewMockRepository(ctrl)

	// Create a new service
	s := notifications.New(validator.New(), repo)

	type want struct {
		notificationID string
	}

	// Test cases
	tests := []struct {
		name  string
		req   *notificationspb.CreateNotificationRequest
		mock  func(req *notificationspb.CreateNotificationRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &notificationspb.CreateNotificationRequest{
				UserId:  "user1",
				Kind:    "kind1",
				Payload: `{"key": "value"}`,
			},
			mock: func(req *notificationspb.CreateNotificationRequest) {
				repo.EXPECT().CreateNotification(
					gomock.Any(),
					req.GetUserId(),
					req.GetKind(),
					req.GetPayload(),
				).Return("notification_id", nil)
			},
			want: want{
				notificationID: "notification_id",
			},
			isErr: false,
		},
		{
			name: "error: missing required fields in request",
			req: &notificationspb.CreateNotificationRequest{
				UserId:  "",
				Kind:    "",
				Payload: ``,
			},
			mock:  func(_ *notificationspb.CreateNotificationRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: invalid user",
			req: &notificationspb.CreateNotificationRequest{
				UserId:  "invalid_user_id",
				Kind:    "kind1",
				Payload: `{"key": "value"}`,
			},
			mock: func(req *notificationspb.CreateNotificationRequest) {
				repo.EXPECT().CreateNotification(
					gomock.Any(),
					req.GetUserId(),
					req.GetKind(),
					req.GetPayload(),
				).Return("", status.Error(codes.InvalidArgument, "invalid user"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: invalid payload",
			req: &notificationspb.CreateNotificationRequest{
				UserId:  "user1",
				Kind:    "kind1",
				Payload: "invalid_payload",
			},
			mock:  func(_ *notificationspb.CreateNotificationRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &notificationspb.CreateNotificationRequest{
				UserId:  "user1",
				Kind:    "kind1",
				Payload: `{"key": "value"}`,
			},
			mock: func(req *notificationspb.CreateNotificationRequest) {
				repo.EXPECT().CreateNotification(
					gomock.Any(),
					req.GetUserId(),
					req.GetKind(),
					req.GetPayload(),
				).Return("", status.Error(codes.Internal, "internal server error"))
			},
			want:  want{},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.mock(tt.req)

			notificationID, err := s.CreateNotification(t.Context(), tt.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			assert.Equal(t, notificationID, tt.want.notificationID)
		})
	}
}

func TestService_MarkNotificationsRead(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := notificationsmock.NewMockRepository(ctrl)

	// Create a new service
	s := notifications.New(validator.New(), repo)

	type want struct {
		err error
	}

	// Test cases
	tests := []struct {
		name  string
		req   *notificationspb.MarkNotificationsReadRequest
		mock  func(req *notificationspb.MarkNotificationsReadRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &notificationspb.MarkNotificationsReadRequest{
				UserId: "user1",
				Ids:    []string{"notification_id_1", "notification_id_2"},
			},
			mock: func(req *notificationspb.MarkNotificationsReadRequest) {
				repo.EXPECT().MarkNotificationsRead(
					gomock.Any(),
					req.GetIds(),
					req.GetUserId(),
				).Return(nil)
			},
			want: want{
				err: nil,
			},
			isErr: false,
		},
		{
			name: "error: missing required fields in request",
			req: &notificationspb.MarkNotificationsReadRequest{
				UserId: "",
				Ids:    nil,
			},
			mock:  func(_ *notificationspb.MarkNotificationsReadRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: some/all notifications not found",
			req: &notificationspb.MarkNotificationsReadRequest{
				UserId: "user1",
				Ids:    []string{"invalid_notification_id"},
			},
			mock: func(req *notificationspb.MarkNotificationsReadRequest) {
				repo.EXPECT().MarkNotificationsRead(
					gomock.Any(),
					req.GetIds(),
					req.GetUserId(),
				).Return(status.Error(codes.NotFound, "some/all notifications not found"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &notificationspb.MarkNotificationsReadRequest{
				UserId: "user1",
				Ids:    []string{"notification_id_1", "notification_id_2"},
			},
			mock: func(req *notificationspb.MarkNotificationsReadRequest) {
				repo.EXPECT().MarkNotificationsRead(
					gomock.Any(),
					req.GetIds(),
					req.GetUserId(),
				).Return(status.Error(codes.Internal, "internal server error"))
			},
			want:  want{},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			err := s.MarkNotificationsRead(t.Context(), tt.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
		})
	}
}

func TestService_ListNotifications(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := notificationsmock.NewMockRepository(ctrl)

	// Create a new service
	s := notifications.New(validator.New(), repo)

	type want struct {
		*notificationsmodel.ListNotificationsResponse
	}

	var (
		createdAt = time.Now()
		updatedAt = time.Now()
		readAt    = sql.NullTime{}
	)

	// Test cases
	tests := []struct {
		name  string
		req   *notificationspb.ListNotificationsRequest
		mock  func(req *notificationspb.ListNotificationsRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &notificationspb.ListNotificationsRequest{
				UserId: "user1",
				Kind:   "",
				Cursor: "",
			},
			mock: func(req *notificationspb.ListNotificationsRequest) {
				repo.EXPECT().ListNotifications(
					gomock.Any(),
					req.GetUserId(),
					req.GetKind(),
					req.GetCursor(),
				).Return(&notificationsmodel.ListNotificationsResponse{
					Notifications: []*notificationsmodel.NotificationResponse{
						{
							ID:        "notification_id_1",
							Kind:      "kind1",
							Payload:   `{"key": "value"}`,
							ReadAt:    readAt,
							CreatedAt: createdAt,
							UpdatedAt: updatedAt,
						},
						{
							ID:        "notification_id_2",
							Kind:      "kind2",
							Payload:   `{"key": "value"}`,
							ReadAt:    readAt,
							CreatedAt: createdAt,
							UpdatedAt: updatedAt,
						},
					},
					Cursor: "",
				}, nil)
			},
			want: want{
				ListNotificationsResponse: &notificationsmodel.ListNotificationsResponse{
					Notifications: []*notificationsmodel.NotificationResponse{
						{
							ID:        "notification_id_1",
							Kind:      "kind1",
							Payload:   `{"key": "value"}`,
							ReadAt:    readAt,
							CreatedAt: createdAt,
							UpdatedAt: updatedAt,
						},
						{
							ID:        "notification_id_2",
							Kind:      "kind2",
							Payload:   `{"key": "value"}`,
							ReadAt:    readAt,
							CreatedAt: createdAt,
							UpdatedAt: updatedAt,
						},
					},
					Cursor: "",
				},
			},
			isErr: false,
		},
		{
			name: "error: missing user ID",
			req: &notificationspb.ListNotificationsRequest{
				UserId: "",
				Kind:   "",
				Cursor: "",
			},
			mock:  func(_ *notificationspb.ListNotificationsRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: user not found",
			req: &notificationspb.ListNotificationsRequest{
				UserId: "invalid_user_id",
				Kind:   "",
				Cursor: "",
			},
			mock: func(req *notificationspb.ListNotificationsRequest) {
				repo.EXPECT().ListNotifications(
					gomock.Any(),
					req.GetUserId(),
					req.GetKind(),
					req.GetCursor(),
				).Return(nil, status.Error(codes.NotFound, "user not found"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &notificationspb.ListNotificationsRequest{
				UserId: "user1",
				Kind:   "",
				Cursor: "",
			},
			mock: func(req *notificationspb.ListNotificationsRequest) {
				repo.EXPECT().ListNotifications(
					gomock.Any(),
					req.GetUserId(),
					req.GetKind(),
					req.GetCursor(),
				).Return(nil, status.Error(codes.Internal, "internal server error"))
			},
			want:  want{},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			notifications, err := s.ListNotifications(t.Context(), tt.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			assert.Equal(t, len(notifications.Notifications), len(tt.want.ListNotificationsResponse.Notifications))

			assert.Equal(t, notifications.Notifications, tt.want.ListNotificationsResponse.Notifications)
			assert.Equal(t, notifications.Cursor, tt.want.ListNotificationsResponse.Cursor)
		})
	}
}
