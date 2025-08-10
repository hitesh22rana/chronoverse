package notifications_test

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"

	"github.com/hitesh22rana/chronoverse/internal/app/notifications"
	notificationsmock "github.com/hitesh22rana/chronoverse/internal/app/notifications/mock"
	notificationsmodel "github.com/hitesh22rana/chronoverse/internal/model/notifications"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
)

func TestMain(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := notificationsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	server := notifications.New(t.Context(), &notifications.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc)

	_ = server
}

func initClient(server *grpc.Server) (client notificationspb.NotificationsServiceClient, _close func()) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	buffer := 1024 * 1024
	lis := bufconn.Listen(buffer)

	go func() {
		if err := server.Serve(lis); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to serve gRPC server: %v\n", err)
		}
	}()

	//nolint:staticcheck // SA1019: This is required for testing.
	conn, err := grpc.DialContext(
		ctx,
		"bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to gRPC server: %v\n", err)
		return nil, nil
	}

	_close = func() {
		err := lis.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to close listener: %v\n", err)
		}

		server.Stop()
	}

	return notificationspb.NewNotificationsServiceClient(conn), _close
}

func TestCreateNotification(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := notificationsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(notifications.New(t.Context(), &notifications.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *notificationspb.CreateNotificationRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*notificationspb.CreateNotificationRequest)
		res   *notificationspb.CreateNotificationResponse
		isErr bool
	}{
		{
			name: "success",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "internal-service",
							),
							auth.RoleAdmin,
						),
						"token",
					)
				},
				req: &notificationspb.CreateNotificationRequest{
					UserId:  "user-id",
					Kind:    "kind",
					Payload: `{"key": "value"}`,
				},
			},
			mock: func(_ *notificationspb.CreateNotificationRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().CreateNotification(
					gomock.Any(),
					gomock.Any(),
				).Return("notification-id", nil)
			},
			res:   &notificationspb.CreateNotificationResponse{},
			isErr: false,
		},
		{
			name: "error: unauthorized access (invalid role)",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "internal-service",
							),
							auth.RoleUser,
						),
						"token",
					)
				},
				req: &notificationspb.CreateNotificationRequest{
					UserId:  "user-id",
					Kind:    "kind",
					Payload: `{"key": "value"}`,
				},
			},
			mock:  func(_ *notificationspb.CreateNotificationRequest) {},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: invalid token",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "internal-service",
							),
							auth.RoleAdmin,
						),
						"invalid-token",
					)
				},
				req: &notificationspb.CreateNotificationRequest{
					UserId:  "user-id",
					Kind:    "kind",
					Payload: `{"key": "value"}`,
				},
			},
			mock: func(_ *notificationspb.CreateNotificationRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, status.Error(codes.Unauthenticated, "invalid token"))
			},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: missing required fields in request",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "internal-service",
							),
							auth.RoleAdmin,
						),
						"token",
					)
				},
				req: &notificationspb.CreateNotificationRequest{
					UserId:  "",
					Kind:    "",
					Payload: ``,
				},
			},
			mock: func(_ *notificationspb.CreateNotificationRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().CreateNotification(
					gomock.Any(),
					gomock.Any(),
				).Return("", status.Error(codes.InvalidArgument, "user_id, kind, and payload are required fields"))
			},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: missing required headers in metadata",
			args: args{
				getCtx: func() context.Context {
					return metadata.AppendToOutgoingContext(
						t.Context(),
					)
				},
				req: &notificationspb.CreateNotificationRequest{
					UserId:  "user-id",
					Kind:    "kind",
					Payload: `{"key": "value"}`,
				},
			},
			mock:  func(_ *notificationspb.CreateNotificationRequest) {},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: internal server error",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "internal-service",
							),
							auth.RoleAdmin,
						),
						"token",
					)
				},
				req: &notificationspb.CreateNotificationRequest{
					UserId:  "user-id",
					Kind:    "kind",
					Payload: `{"key": "value"}`,
				},
			},
			mock: func(_ *notificationspb.CreateNotificationRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().CreateNotification(
					gomock.Any(),
					gomock.Any(),
				).Return("", status.Error(codes.Internal, "internal server error"))
			},
			res:   nil,
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.args.req)
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.CreateNotification(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("CreateNotification() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("CreateNotification() error = nil, want error")
				}
				return
			}
		})
	}
}

func TestMarkNotificationsRead(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := notificationsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(notifications.New(t.Context(), &notifications.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *notificationspb.MarkNotificationsReadRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*notificationspb.MarkNotificationsReadRequest)
		res   *notificationspb.MarkNotificationsReadResponse
		isErr bool
	}{
		{
			name: "success",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "server-test",
							),
							auth.RoleUser,
						),
						"token",
					)
				},
				req: &notificationspb.MarkNotificationsReadRequest{
					Ids:    []string{"notification-id-1", "notification-id-2"},
					UserId: "user-id",
				},
			},
			mock: func(_ *notificationspb.MarkNotificationsReadRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().MarkNotificationsRead(
					gomock.Any(),
					gomock.Any(),
				).Return(nil)
			},
			res:   &notificationspb.MarkNotificationsReadResponse{},
			isErr: false,
		},
		{
			name: "error: invalid token",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "server-test",
							),
							auth.RoleUser,
						),
						"invalid-token",
					)
				},
				req: &notificationspb.MarkNotificationsReadRequest{
					Ids:    []string{"notification-id-1", "notification-id-2"},
					UserId: "user-id",
				},
			},
			mock: func(_ *notificationspb.MarkNotificationsReadRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, status.Error(codes.Unauthenticated, "invalid token"))
			},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: missing required fields in request",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "server-test",
							),
							auth.RoleUser,
						),
						"token",
					)
				},
				req: &notificationspb.MarkNotificationsReadRequest{
					Ids:    []string{},
					UserId: "",
				},
			},
			mock: func(_ *notificationspb.MarkNotificationsReadRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().MarkNotificationsRead(
					gomock.Any(),
					gomock.Any(),
				).Return(status.Error(codes.InvalidArgument, "user_id and ids are required fields"))
			},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: missing required headers in metadata",
			args: args{
				getCtx: func() context.Context {
					return metadata.AppendToOutgoingContext(
						t.Context(),
					)
				},
				req: &notificationspb.MarkNotificationsReadRequest{
					Ids:    []string{"notification-id-1", "notification-id-2"},
					UserId: "user-id",
				},
			},
			mock:  func(_ *notificationspb.MarkNotificationsReadRequest) {},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: internal server error",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "server-test",
							),
							auth.RoleUser,
						),
						"token",
					)
				},
				req: &notificationspb.MarkNotificationsReadRequest{
					Ids:    []string{"notification-id-1", "notification-id-2"},
					UserId: "user-id",
				},
			},
			mock: func(_ *notificationspb.MarkNotificationsReadRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().MarkNotificationsRead(
					gomock.Any(),
					gomock.Any(),
				).Return(status.Error(codes.Internal, "internal server error"))
			},
			res:   nil,
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.args.req)
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.MarkNotificationsRead(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("MarkNotificationsRead() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("MarkNotificationsRead() error = nil, want error")
				}
				return
			}
		})
	}
}

func TestListNotifications(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := notificationsmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(notifications.New(t.Context(), &notifications.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *notificationspb.ListNotificationsRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*notificationspb.ListNotificationsRequest)
		res   *notificationspb.ListNotificationsResponse
		isErr bool
	}{
		{
			name: "success",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "server-test",
							),
							auth.RoleUser,
						),
						"token",
					)
				},
				req: &notificationspb.ListNotificationsRequest{
					UserId: "user-id",
					Cursor: "",
				},
			},
			mock: func(_ *notificationspb.ListNotificationsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().ListNotifications(
					gomock.Any(),
					gomock.Any(),
				).Return(&notificationsmodel.ListNotificationsResponse{
					Notifications: []*notificationsmodel.NotificationResponse{
						{
							ID:        "notification-id",
							Kind:      "kind",
							Payload:   `{"key": "value"}`,
							ReadAt:    sql.NullTime{},
							CreatedAt: time.Now(),
							UpdatedAt: time.Now(),
						},
						{
							ID:        "notification-id-2",
							Kind:      "kind-2",
							Payload:   `{"key": "value"}`,
							ReadAt:    sql.NullTime{},
							CreatedAt: time.Now(),
							UpdatedAt: time.Now(),
						},
					},
					Cursor: "cursor",
				}, nil)
			},
			res: &notificationspb.ListNotificationsResponse{
				Notifications: []*notificationspb.NotificationResponse{
					{
						Id:        "notification-id",
						Kind:      "kind",
						Payload:   `{"key": "value"}`,
						ReadAt:    "",
						CreatedAt: time.Now().Format(time.RFC3339Nano),
						UpdatedAt: time.Now().Format(time.RFC3339Nano),
					},
					{
						Id:        "notification-id-2",
						Kind:      "kind-2",
						Payload:   `{"key": "value"}`,
						ReadAt:    "",
						CreatedAt: time.Now().Format(time.RFC3339Nano),
						UpdatedAt: time.Now().Format(time.RFC3339Nano),
					},
				},
			},
			isErr: false,
		},
		{
			name: "error: invalid token",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "server-test",
							),
							auth.RoleUser,
						),
						"invalid-token",
					)
				},
				req: &notificationspb.ListNotificationsRequest{
					UserId: "user-id",
					Cursor: "",
				},
			},
			mock: func(_ *notificationspb.ListNotificationsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, status.Error(codes.Unauthenticated, "invalid token"))
			},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: missing required fields in request",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "server-test",
							),
							auth.RoleUser,
						),
						"token",
					)
				},
				req: &notificationspb.ListNotificationsRequest{
					UserId: "",
					Cursor: "",
				},
			},
			mock: func(_ *notificationspb.ListNotificationsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().ListNotifications(
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.InvalidArgument, "user_id is required field"))
			},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: missing required headers in metadata",
			args: args{
				getCtx: func() context.Context {
					return metadata.AppendToOutgoingContext(
						t.Context(),
					)
				},
				req: &notificationspb.ListNotificationsRequest{
					UserId: "user-id",
					Cursor: "",
				},
			},
			mock:  func(_ *notificationspb.ListNotificationsRequest) {},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: internal server error",
			args: args{
				getCtx: func() context.Context {
					return auth.WithAuthorizationTokenInMetadata(
						auth.WithRoleInMetadata(
							auth.WithAudienceInMetadata(
								t.Context(), "server-test",
							),
							auth.RoleUser,
						),
						"token",
					)
				},
				req: &notificationspb.ListNotificationsRequest{
					UserId: "user-id",
					Cursor: "",
				},
			},
			mock: func(_ *notificationspb.ListNotificationsRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().ListNotifications(
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.Internal, "internal server error"))
			},
			res:   nil,
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.args.req)
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.ListNotifications(tt.args.getCtx(), tt.args.req)
			if (err != nil) != tt.isErr {
				t.Errorf("ListNotifications() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if tt.isErr {
				if err == nil {
					t.Error("ListNotifications() error = nil, want error")
				}
				return
			}
		})
	}
}
