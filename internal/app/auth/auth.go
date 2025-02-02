//go:generate go run go.uber.org/mock/mockgen@v0.5.0 -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package auth

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	pb "github.com/hitesh22rana/chronoverse/pkg/proto/go"
)

// Service provides user authentication operations.
type Service interface {
	Register(ctx context.Context, email, password string) (string, error)
	Login(ctx context.Context, email, password string) (string, error)
	Logout(ctx context.Context, token string) error
	ValidateToken(ctx context.Context, token string) error
}

// Auth represents the authentication application.
type Auth struct {
	pb.UnimplementedAuthServiceServer
	log *zap.Logger
	svc Service
}

// New creates a new authentication server.
func New(log *zap.Logger, svc Service) *grpc.Server {
	server := grpc.NewServer()
	pb.RegisterAuthServiceServer(server, &Auth{
		log: log,
		svc: svc,
	})

	reflection.Register(server)
	return server
}

// Register registers a new user.
func (a *Auth) Register(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
	resp, err := a.svc.Register(ctx, req.GetEmail(), req.GetPassword())
	if err != nil {
		a.log.Error("failed to register user", zap.Error(err))
		return nil, fmt.Errorf("failed to register user: %w", err)
	}

	a.log.Info("user registered successfully", zap.String("email", req.GetEmail()))
	return &pb.RegisterResponse{Token: resp}, nil
}

// Login logs in the user.
func (a *Auth) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error) {
	resp, err := a.svc.Login(ctx, req.GetEmail(), req.GetPassword())
	if err != nil {
		a.log.Error("failed to login user", zap.Error(err))
		return nil, fmt.Errorf("failed to login user: %w", err)
	}

	a.log.Info("user logged in successfully", zap.String("email", req.GetEmail()))
	return &pb.LoginResponse{Token: resp}, nil
}

// Logout logs out the user.
func (a *Auth) Logout(ctx context.Context, req *pb.LogoutRequest) (*pb.LogoutResponse, error) {
	err := a.svc.Logout(ctx, req.GetToken())
	if err != nil {
		a.log.Error("failed to logout user", zap.Error(err))
		return nil, fmt.Errorf("failed to logout user: %w", err)
	}

	a.log.Info("user logged out successfully", zap.String("token", req.GetToken()))
	return &pb.LogoutResponse{}, nil
}

// ValidateToken validates the token.
func (a *Auth) ValidateToken(ctx context.Context, req *pb.ValidateTokenRequest) (*pb.ValidateTokenResponse, error) {
	err := a.svc.ValidateToken(ctx, req.GetToken())
	if err != nil {
		a.log.Error("failed to validate token", zap.Error(err))
		return nil, fmt.Errorf("failed to validate token: %w", err)
	}

	a.log.Info("token validated successfully", zap.String("token", req.GetToken()))
	return &pb.ValidateTokenResponse{}, nil
}
