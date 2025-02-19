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

	pb "github.com/hitesh22rana/chronoverse/pkg/proto/go"

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

func initClient(server *grpc.Server) (client pb.UsersServiceClient, _close func()) {
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

	return pb.NewUsersServiceClient(conn), _close
}

func TestRegister(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := usersmock.NewMockService(ctrl)

	client, _close := initClient(users.New(context.Background(), &users.Config{
		Deadline: 500 * time.Millisecond,
	}, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *pb.RegisterRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*pb.RegisterRequest)
		res   *pb.RegisterResponse
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
				req: &pb.RegisterRequest{
					Email:    "test@gmail.com",
					Password: "password12345",
				},
			},
			mock: func(req *pb.RegisterRequest) {
				svc.EXPECT().Register(
					gomock.Any(),
					req.GetEmail(),
					req.GetPassword(),
				).Return("user1", "pat1", nil)
			},
			res:   &pb.RegisterResponse{},
			isErr: false,
		},
		{
			name: "error: email and password are required",
			args: args{
				getCtx: func() context.Context {
					return auth.WithRoleInMetadata(
						auth.WithAudienceInMetadata(
							context.Background(), "users-test",
						),
						auth.RoleUser,
					)
				},
				req: &pb.RegisterRequest{
					Email:    "",
					Password: "",
				},
			},
			mock: func(req *pb.RegisterRequest) {
				svc.EXPECT().Register(
					gomock.Any(),
					req.GetEmail(),
					req.GetPassword(),
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
				req: &pb.RegisterRequest{
					Email:    "test@gmail.com",
					Password: "password12345",
				},
			},
			mock: func(req *pb.RegisterRequest) {
				svc.EXPECT().Register(
					gomock.Any(),
					req.GetEmail(),
					req.GetPassword(),
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
				req: &pb.RegisterRequest{
					Email:    "test@gmail.com",
					Password: "password12345",
				},
			},
			mock: func(req *pb.RegisterRequest) {
				svc.EXPECT().Register(
					gomock.Any(),
					req.GetEmail(),
					req.GetPassword(),
				).Return("", "", status.Errorf(codes.Internal, "internal server error"))
			},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: missing audience in context",
			args: args{
				getCtx: func() context.Context {
					return metadata.AppendToOutgoingContext(
						context.Background(),
					)
				},
				req: &pb.RegisterRequest{},
			},
			mock:  func(_ *pb.RegisterRequest) {},
			res:   nil,
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.args.req)
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.Register(tt.args.getCtx(), tt.args.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
		})
	}
}

func TestLogin(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := usersmock.NewMockService(ctrl)

	client, _close := initClient(users.New(context.Background(), &users.Config{
		Deadline: 500 * time.Millisecond,
	}, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *pb.LoginRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func(*pb.LoginRequest)
		res   *pb.LoginResponse
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
				req: &pb.LoginRequest{
					Email:    "test@gmail.com",
					Password: "password12345",
				},
			},
			mock: func(req *pb.LoginRequest) {
				svc.EXPECT().Login(
					gomock.Any(),
					req.GetEmail(),
					req.GetPassword(),
				).Return("user1", "pat1", nil)
			},
			res:   &pb.LoginResponse{},
			isErr: false,
		},
		{
			name: "error: email and password are required",
			args: args{
				getCtx: func() context.Context {
					return auth.WithRoleInMetadata(
						auth.WithAudienceInMetadata(
							context.Background(), "users-test",
						),
						auth.RoleUser,
					)
				},
				req: &pb.LoginRequest{
					Email:    "",
					Password: "",
				},
			},
			mock: func(req *pb.LoginRequest) {
				svc.EXPECT().Login(
					gomock.Any(),
					req.GetEmail(),
					req.GetPassword(),
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
				req: &pb.LoginRequest{
					Email:    "test1@gmail.com",
					Password: "password123451",
				},
			},
			mock: func(req *pb.LoginRequest) {
				svc.EXPECT().Login(
					gomock.Any(),
					req.GetEmail(),
					req.GetPassword(),
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
				req: &pb.LoginRequest{
					Email:    "test@gmail.com",
					Password: "password12345",
				},
			},
			mock: func(req *pb.LoginRequest) {
				svc.EXPECT().Login(
					gomock.Any(),
					req.GetEmail(),
					req.GetPassword(),
				).Return("", "", status.Errorf(codes.Internal, "internal server error"))
			},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: missing audience in context",
			args: args{
				getCtx: func() context.Context {
					return metadata.AppendToOutgoingContext(
						context.Background(),
					)
				},
				req: &pb.LoginRequest{},
			},
			mock:  func(_ *pb.LoginRequest) {},
			res:   nil,
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.args.req)
		t.Run(tt.name, func(t *testing.T) {
			var headers metadata.MD
			_, err := client.Login(tt.args.getCtx(), tt.args.req, grpc.Header(&headers))
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
		})
	}
}
