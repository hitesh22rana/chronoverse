package auth

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	pb "github.com/hitesh22rana/chronoverse/pkg/proto/auth"
)

//go:generate go run go.uber.org/mock/mockgen@v0.5.0 -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE
type Service interface {
	Register(ctx context.Context, email string, password string) (string, error)
	Login(ctx context.Context, email string, password string) (string, error)
	Logout(ctx context.Context, token string) error
}

type Application struct {
	pb.UnimplementedAuthServiceServer
	log *zap.Logger
	svc Service
}

func New(log *zap.Logger, svc Service) *grpc.Server {
	server := grpc.NewServer()
	pb.RegisterAuthServiceServer(server, &Application{
		log: log,
		svc: svc,
	})

	reflection.Register(server)
	return server
}

func (a *Application) Register(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
	resp, err := a.svc.Register(ctx, req.GetEmail(), req.GetPassword())
	if err != nil {
		a.log.Error("failed to register user", zap.Error(err))
		return nil, err
	}

	a.log.Info("user registered successfully", zap.String("email", req.GetEmail()))
	return &pb.RegisterResponse{Token: resp}, nil
}

func (a *Application) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error) {
	resp, err := a.svc.Login(ctx, req.GetEmail(), req.GetPassword())
	if err != nil {
		a.log.Error("failed to login user", zap.Error(err))
		return nil, err
	}

	a.log.Info("user logged in successfully", zap.String("email", req.GetEmail()))
	return &pb.LoginResponse{Token: resp}, nil
}

func (a *Application) Logout(ctx context.Context, req *pb.LogoutRequest) (*pb.LogoutResponse, error) {
	err := a.svc.Logout(ctx, req.GetToken())
	if err != nil {
		a.log.Error("failed to logout user", zap.Error(err))
		return nil, err
	}

	a.log.Info("user logged out successfully", zap.String("token", req.GetToken()))
	return &pb.LogoutResponse{}, nil
}
