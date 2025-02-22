package users_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

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
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
)

func TestMain(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := usersmock.NewMockService(ctrl)

	server := users.New(context.Background(), &users.Config{
		Deadline: 500 * time.Millisecond,
	}, svc)

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

	client, _close := initClient(users.New(context.Background(), &users.Config{
		Deadline: 500 * time.Millisecond,
	}, svc))
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
							context.Background(), "users-test",
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
							context.Background(), "users-test",
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
							context.Background(), "users-test",
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
							context.Background(), "users-test",
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
						context.Background(),
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

	client, _close := initClient(users.New(context.Background(), &users.Config{
		Deadline: 500 * time.Millisecond,
	}, svc))
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
							context.Background(), "users-test",
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
							context.Background(), "users-test",
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
							context.Background(), "users-test",
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
							context.Background(), "users-test",
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
						context.Background(),
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
