package users_test

import (
	"context"
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

	userpb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"

	"github.com/hitesh22rana/chronoverse/internal/app/users"
	usersmock "github.com/hitesh22rana/chronoverse/internal/app/users/mock"
	usersmodel "github.com/hitesh22rana/chronoverse/internal/model/users"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
)

func TestMain(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := usersmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	server := users.New(t.Context(), &users.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc)

	_ = server
}

func initClient(server *grpc.Server) (client userpb.UsersServiceClient, _close func()) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	buffer := 1024 * 1024
	lis := bufconn.Listen(buffer)

	go func() {
		if err := server.Serve(lis); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to serve gRPC server: %v\n", err)
		}
	}()

	//nolint:staticcheck // This is required for testing.
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

	return userpb.NewUsersServiceClient(conn), _close
}

func TestRegisterUser(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := usersmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(users.New(t.Context(), &users.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *userpb.RegisterUserRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*userpb.RegisterUserRequest)
		res   *userpb.RegisterUserResponse
		isErr bool
	}{
		{
			name: "success",
			args: args{
				getCtx: func() context.Context {
					return auth.WithRoleInMetadata(
						auth.WithAudienceInMetadata(
							t.Context(), "server-test",
						),
						auth.RoleUser,
					)
				},
				req: &userpb.RegisterUserRequest{
					Email:    "test@gmail.com",
					Password: "password12345",
				},
			},
			mock: func(_ *userpb.RegisterUserRequest) {
				svc.EXPECT().RegisterUser(
					gomock.Any(),
					gomock.Any(),
				).Return("user1", "pat1", nil)
			},
			res: &userpb.RegisterUserResponse{
				UserId: "user1",
			},
			isErr: false,
		},
		{
			name: "error: missing required fields in request",
			args: args{
				getCtx: func() context.Context {
					return auth.WithRoleInMetadata(
						auth.WithAudienceInMetadata(
							t.Context(), "server-test",
						),
						auth.RoleUser,
					)
				},
				req: &userpb.RegisterUserRequest{
					Email:    "",
					Password: "",
				},
			},
			mock: func(_ *userpb.RegisterUserRequest) {
				svc.EXPECT().RegisterUser(
					gomock.Any(),
					gomock.Any(),
				).Return("", "", status.Errorf(codes.InvalidArgument, "email and password are required"))
			},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: user already exists",
			args: args{
				getCtx: func() context.Context {
					return auth.WithRoleInMetadata(
						auth.WithAudienceInMetadata(
							t.Context(), "server-test",
						),
						auth.RoleUser,
					)
				},
				req: &userpb.RegisterUserRequest{
					Email:    "test@gmail.com",
					Password: "password12345",
				},
			},
			mock: func(_ *userpb.RegisterUserRequest) {
				svc.EXPECT().RegisterUser(
					gomock.Any(),
					gomock.Any(),
				).Return("", "", status.Errorf(codes.AlreadyExists, "user already exists"))
			},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: internal server error",
			args: args{
				getCtx: func() context.Context {
					return auth.WithRoleInMetadata(
						auth.WithAudienceInMetadata(
							t.Context(), "server-test",
						),
						auth.RoleUser,
					)
				},
				req: &userpb.RegisterUserRequest{
					Email:    "test@gmail.com",
					Password: "password12345",
				},
			},
			mock: func(_ *userpb.RegisterUserRequest) {
				svc.EXPECT().RegisterUser(
					gomock.Any(),
					gomock.Any(),
				).Return("", "", status.Errorf(codes.Internal, "internal server error"))
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
				req: &userpb.RegisterUserRequest{
					Email:    "test@gmail.com",
					Password: "password12345",
				},
			},
			mock:  func(_ *userpb.RegisterUserRequest) {},
			res:   nil,
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.args.req)
		t.Run(tt.name, func(t *testing.T) {
			var headers metadata.MD
			_, err := client.RegisterUser(tt.args.getCtx(), tt.args.req, grpc.Header(&headers))
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
		})
	}
}

func TestLoginUser(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := usersmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(users.New(t.Context(), &users.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *userpb.LoginUserRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*userpb.LoginUserRequest)
		res   *userpb.LoginUserResponse
		isErr bool
	}{
		{
			name: "success",
			args: args{
				getCtx: func() context.Context {
					return auth.WithRoleInMetadata(
						auth.WithAudienceInMetadata(
							t.Context(), "server-test",
						),
						auth.RoleUser,
					)
				},
				req: &userpb.LoginUserRequest{
					Email:    "test@gmail.com",
					Password: "password12345",
				},
			},
			mock: func(_ *userpb.LoginUserRequest) {
				svc.EXPECT().LoginUser(
					gomock.Any(),
					gomock.Any(),
				).Return("user1", "pat1", nil)
			},
			res:   &userpb.LoginUserResponse{},
			isErr: false,
		},
		{
			name: "error: missing required fields in request",
			args: args{
				getCtx: func() context.Context {
					return auth.WithRoleInMetadata(
						auth.WithAudienceInMetadata(
							t.Context(), "server-test",
						),
						auth.RoleUser,
					)
				},
				req: &userpb.LoginUserRequest{
					Email:    "",
					Password: "",
				},
			},
			mock: func(_ *userpb.LoginUserRequest) {
				svc.EXPECT().LoginUser(
					gomock.Any(),
					gomock.Any(),
				).Return("", "", status.Errorf(codes.InvalidArgument, "email and password are required"))
			},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: user does not exist",
			args: args{
				getCtx: func() context.Context {
					return auth.WithRoleInMetadata(
						auth.WithAudienceInMetadata(
							t.Context(), "server-test",
						),
						auth.RoleUser,
					)
				},
				req: &userpb.LoginUserRequest{
					Email:    "test1@gmail.com",
					Password: "password123451",
				},
			},
			mock: func(_ *userpb.LoginUserRequest) {
				svc.EXPECT().LoginUser(
					gomock.Any(),
					gomock.Any(),
				).Return("", "", status.Errorf(codes.AlreadyExists, "user already exists"))
			},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: internal server error",
			args: args{
				getCtx: func() context.Context {
					return auth.WithRoleInMetadata(
						auth.WithAudienceInMetadata(
							t.Context(), "server-test",
						),
						auth.RoleUser,
					)
				},
				req: &userpb.LoginUserRequest{
					Email:    "test@gmail.com",
					Password: "password12345",
				},
			},
			mock: func(_ *userpb.LoginUserRequest) {
				svc.EXPECT().LoginUser(
					gomock.Any(),
					gomock.Any(),
				).Return("", "", status.Errorf(codes.Internal, "internal server error"))
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
				req: &userpb.LoginUserRequest{},
			},
			mock:  func(_ *userpb.LoginUserRequest) {},
			res:   nil,
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.args.req)
		t.Run(tt.name, func(t *testing.T) {
			var headers metadata.MD
			_, err := client.LoginUser(tt.args.getCtx(), tt.args.req, grpc.Header(&headers))
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
		})
	}
}

func TestGetUser(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := usersmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(users.New(t.Context(), &users.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *userpb.GetUserRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*userpb.GetUserRequest)
		res   *userpb.GetUserResponse
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
				req: &userpb.GetUserRequest{
					Id: "user1",
				},
			},
			mock: func(_ *userpb.GetUserRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetUser(
					gomock.Any(),
					gomock.Any(),
				).Return(&usersmodel.GetUserResponse{
					ID:                     "user1",
					Email:                  "user1@example.com",
					NotificationPreference: "ALERTS",
					CreatedAt:              time.Now(),
					UpdatedAt:              time.Now(),
				}, nil)
			},
			res: &userpb.GetUserResponse{
				Email:                  "user1@example.com",
				NotificationPreference: "ALERTS",
				CreatedAt:              time.Now().Format(time.RFC3339Nano),
				UpdatedAt:              time.Now().Format(time.RFC3339Nano),
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
				req: &userpb.GetUserRequest{
					Id: "user1",
				},
			},
			mock: func(_ *userpb.GetUserRequest) {
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
				req: &userpb.GetUserRequest{
					Id: "",
				},
			},
			mock: func(_ *userpb.GetUserRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetUser(
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Errorf(codes.InvalidArgument, "user ID is required"))
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
				req: &userpb.GetUserRequest{
					Id: "user1",
				},
			},
			mock:  func(_ *userpb.GetUserRequest) {},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: user not found",
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
				req: &userpb.GetUserRequest{
					Id: "user1",
				},
			},
			mock: func(_ *userpb.GetUserRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetUser(
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Errorf(codes.NotFound, "user not found"))
			},
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
				req: &userpb.GetUserRequest{
					Id: "user1",
				},
			},
			mock: func(_ *userpb.GetUserRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().GetUser(
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Errorf(codes.Internal, "internal server error"))
			},
			res:   nil,
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.args.req)
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.GetUser(tt.args.getCtx(), tt.args.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
		})
	}
}

func TestUpdateUser(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := usersmock.NewMockService(ctrl)
	_auth := authmock.NewMockIAuth(ctrl)

	client, _close := initClient(users.New(t.Context(), &users.Config{
		Deadline: 500 * time.Millisecond,
	}, _auth, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *userpb.UpdateUserRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*userpb.UpdateUserRequest)
		res   *userpb.UpdateUserResponse
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
				req: &userpb.UpdateUserRequest{
					Id:                     "user1",
					Password:               "password12345",
					NotificationPreference: "ALERTS",
				},
			},
			mock: func(_ *userpb.UpdateUserRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().UpdateUser(
					gomock.Any(),
					gomock.Any(),
				).Return(nil)
			},
			res:   &userpb.UpdateUserResponse{},
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
				req: &userpb.UpdateUserRequest{
					Id:                     "user1",
					Password:               "password12345",
					NotificationPreference: "ALERTS",
				},
			},
			mock: func(_ *userpb.UpdateUserRequest) {
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
				req: &userpb.UpdateUserRequest{
					Id:                     "",
					Password:               "",
					NotificationPreference: "",
				},
			},
			mock: func(_ *userpb.UpdateUserRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().UpdateUser(
					gomock.Any(),
					gomock.Any(),
				).Return(status.Errorf(codes.InvalidArgument, "user ID, password and notification preference are required"))
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
				req: &userpb.UpdateUserRequest{
					Id:                     "user1",
					Password:               "password12345",
					NotificationPreference: "ALERTS",
				},
			},
			mock:  func(_ *userpb.UpdateUserRequest) {},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: user not found",
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
				req: &userpb.UpdateUserRequest{
					Id:                     "user1",
					Password:               "password12345",
					NotificationPreference: "ALERTS",
				},
			},
			mock: func(_ *userpb.UpdateUserRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().UpdateUser(
					gomock.Any(),
					gomock.Any(),
				).Return(status.Errorf(codes.NotFound, "user not found"))
			},
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
				req: &userpb.UpdateUserRequest{
					Id:                     "user1",
					Password:               "password12345",
					NotificationPreference: "ALERTS",
				},
			},
			mock: func(_ *userpb.UpdateUserRequest) {
				_auth.EXPECT().ValidateToken(gomock.Any()).Return(&jwt.Token{}, nil)
				svc.EXPECT().UpdateUser(
					gomock.Any(),
					gomock.Any(),
				).Return(status.Errorf(codes.Internal, "internal server error"))
			},
			res:   nil,
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.args.req)
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.UpdateUser(tt.args.getCtx(), tt.args.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
		})
	}
}
