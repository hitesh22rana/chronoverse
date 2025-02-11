package auth_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"testing"

	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "github.com/hitesh22rana/chronoverse/pkg/proto/go"

	"github.com/hitesh22rana/chronoverse/internal/app/auth"
	authmock "github.com/hitesh22rana/chronoverse/internal/app/auth/mock"
)

func TestMain(t *testing.T) {
	ctrl := gomock.NewController(t)

	logger := zap.NewNop()
	svc := authmock.NewMockService(ctrl)

	server := auth.New(logger, svc)

	_ = server
}

func initClient(server *grpc.Server) (client pb.AuthServiceClient, _close func()) {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", 51234))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create listener: %v\n", err)
	}

	errChan := make(chan error, 1)
	go func() {
		if err = server.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			errChan <- status.Errorf(codes.Internal, "failed to serve: %v", err)
		}
		close(errChan)
	}()

	conn, err := grpc.NewClient(
		"localhost:51234",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to gRPC server: %v\n", err)
		return nil, nil
	}

	_close = func() {
		server.Stop()
		conn.Close()
		listener.Close()

		// Wait for any server errors
		if err := <-errChan; err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
		}
	}

	client = pb.NewAuthServiceClient(conn)
	return client, _close
}

func TestRegister(t *testing.T) {
	ctrl := gomock.NewController(t)

	logger := zap.NewNop()
	svc := authmock.NewMockService(ctrl)

	client, _close := initClient(auth.New(logger, svc))
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
					return metadata.AppendToOutgoingContext(
						context.Background(),
						"audience", "auth-test",
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
			res: &pb.RegisterResponse{
				Pat: "pat1",
			},
			isErr: false,
		},
		{
			name: "error: email and password are required",
			args: args{
				getCtx: func() context.Context {
					return metadata.AppendToOutgoingContext(
						context.Background(),
						"audience", "auth-test",
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
					return metadata.AppendToOutgoingContext(
						context.Background(),
						"audience", "auth-test",
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
					return metadata.AppendToOutgoingContext(
						context.Background(),
						"audience", "auth-test",
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
			out, err := client.Register(tt.args.getCtx(), tt.args.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if tt.res.Pat != out.Pat {
				t.Errorf("expected %s, got %s", tt.res.Pat, out.Pat)
			}
		})
	}
}

func TestLogin(t *testing.T) {
	ctrl := gomock.NewController(t)

	logger := zap.NewNop()
	svc := authmock.NewMockService(ctrl)

	client, _close := initClient(auth.New(logger, svc))
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
					return metadata.AppendToOutgoingContext(
						context.Background(),
						"audience", "auth-test",
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
			res: &pb.LoginResponse{
				Pat: "pat1",
			},
			isErr: false,
		},
		{
			name: "error: email and password are required",
			args: args{
				getCtx: func() context.Context {
					return metadata.AppendToOutgoingContext(
						context.Background(),
						"audience", "auth-test",
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
					return metadata.AppendToOutgoingContext(
						context.Background(),
						"audience", "auth-test",
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
					return metadata.AppendToOutgoingContext(
						context.Background(),
						"audience", "auth-test",
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
			out, err := client.Login(tt.args.getCtx(), tt.args.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if tt.res.Pat != out.Pat {
				t.Errorf("expected %s, got %s", tt.res.Pat, out.Pat)
			}
		})
	}
}

func TestLogout(t *testing.T) {
	ctrl := gomock.NewController(t)

	logger := zap.NewNop()
	svc := authmock.NewMockService(ctrl)

	client, _close := initClient(auth.New(logger, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *pb.LogoutRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func()
		res   *pb.LogoutResponse
		isErr bool
	}{
		{
			name: "success",
			args: args{
				getCtx: func() context.Context {
					return metadata.AppendToOutgoingContext(
						context.Background(),
						"audience", "auth-test",
						"pat", "pat",
					)
				},
				req: &pb.LogoutRequest{},
			},
			mock: func() {
				svc.EXPECT().Logout(
					gomock.Any(),
				).Return("user1", nil)
			},
			res:   &pb.LogoutResponse{},
			isErr: false,
		},
		{
			name: "error: pat is required",
			args: args{
				getCtx: func() context.Context {
					return metadata.AppendToOutgoingContext(
						context.Background(),
						"audience", "auth-test",
					)
				},
				req: &pb.LogoutRequest{},
			},
			mock:  func() {},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: pat is already revoked",
			args: args{
				getCtx: func() context.Context {
					return metadata.AppendToOutgoingContext(
						context.Background(),
						"audience", "auth-test",
						"pat", "revoked-pat",
					)
				},
				req: &pb.LogoutRequest{},
			},
			mock: func() {
				svc.EXPECT().Logout(
					gomock.Any(),
				).Return("", status.Error(codes.Unauthenticated, "pat is already revoked"))
			},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: internal server error",
			args: args{
				getCtx: func() context.Context {
					return metadata.AppendToOutgoingContext(
						context.Background(),
						"audience", "auth-test",
						"pat", "pat",
					)
				},
				req: &pb.LogoutRequest{},
			},
			mock: func() {
				svc.EXPECT().Logout(
					gomock.Any(),
				).Return("", status.Errorf(codes.Internal, "internal server error"))
			},
			res:   nil,
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock()
		t.Run(tt.name, func(t *testing.T) {
			res, err := client.Logout(tt.args.getCtx(), tt.args.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if tt.res != nil {
				if res == nil {
					t.Errorf("expected response, got nil")
				}
			}
		})
	}
}

func TestValidate(t *testing.T) {
	ctrl := gomock.NewController(t)

	logger := zap.NewNop()
	svc := authmock.NewMockService(ctrl)

	client, _close := initClient(auth.New(logger, svc))
	defer _close()

	type args struct {
		getCtx func() context.Context
		req    *pb.ValidateRequest
	}

	tests := []struct {
		name  string
		args  args
		mock  func()
		res   *pb.ValidateResponse
		isErr bool
	}{
		{
			name: "success",
			args: args{
				getCtx: func() context.Context {
					return metadata.AppendToOutgoingContext(
						context.Background(),
						"audience", "auth-test",
						"pat", "pat",
					)
				},
				req: &pb.ValidateRequest{},
			},
			mock: func() {
				svc.EXPECT().Validate(gomock.Any()).Return("user1", nil)
			},
			res:   &pb.ValidateResponse{},
			isErr: false,
		},
		{
			name: "error: pat is required",
			args: args{
				getCtx: func() context.Context {
					return metadata.AppendToOutgoingContext(
						context.Background(),
						"audience", "auth-test",
					)
				},
				req: &pb.ValidateRequest{},
			},
			mock:  func() {},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: pat is already revoked",
			args: args{
				getCtx: func() context.Context {
					return metadata.AppendToOutgoingContext(
						context.Background(),
						"audience", "auth-test",
						"pat", "revoked-pat",
					)
				},
				req: &pb.ValidateRequest{},
			},
			mock: func() {
				svc.EXPECT().Validate(
					gomock.Any(),
				).Return("", status.Error(codes.Unauthenticated, "pat is already revoked"))
			},
			res:   nil,
			isErr: true,
		},
		{
			name: "error: internal server error",
			args: args{
				getCtx: func() context.Context {
					return metadata.AppendToOutgoingContext(
						context.Background(),
						"audience", "auth-test",
						"pat", "pat",
					)
				},
				req: &pb.ValidateRequest{},
			},
			mock: func() {
				svc.EXPECT().Validate(
					gomock.Any(),
				).Return("", status.Errorf(codes.Internal, "internal server error"))
			},
			res:   nil,
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock()
		t.Run(tt.name, func(t *testing.T) {
			res, err := client.Validate(tt.args.getCtx(), tt.args.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if tt.res != nil {
				if res == nil {
					t.Errorf("expected response, got nil")
				}
			}
		})
	}
}
