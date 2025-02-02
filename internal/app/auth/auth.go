//go:generate go run go.uber.org/mock/mockgen@v0.5.0 -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package auth

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	pb "github.com/hitesh22rana/chronoverse/pkg/proto/go"
)

// Service provides user authentication operations.
type Service interface {
	Register(ctx context.Context, email, password string) (string, string, error)
	Login(ctx context.Context, email, password string) (string, string, error)
	Logout(ctx context.Context, token string) error
	Validate(ctx context.Context, token string) error
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
	userID, pat, err := a.svc.Register(ctx, req.GetEmail(), req.GetPassword())
	if err != nil {
		if status.Code(err) == codes.Internal {
			a.log.Error("failed to register user", zap.Error(err))
		} else {
			a.log.Warn("failed to register user", zap.Error(err))
		}
		return nil, err
	}

	a.log.Info("user registered successfully", zap.String("user_id", userID))
	return &pb.RegisterResponse{UserId: userID, Pat: pat}, nil
}

// Login logs in the user.
func (a *Auth) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error) {
	userID, pat, err := a.svc.Login(ctx, req.GetEmail(), req.GetPassword())
	if err != nil {
		if status.Code(err) == codes.Internal {
			a.log.Error("failed to login user", zap.Error(err))
		} else {
			a.log.Warn("failed to login user", zap.Error(err))
		}
		return nil, err
	}

	a.log.Info("user logged in successfully", zap.String("user_id", userID))
	return &pb.LoginResponse{UserId: userID, Pat: pat}, nil
}

// Logout logs out the user.
func (a *Auth) Logout(ctx context.Context, req *pb.LogoutRequest) (*pb.LogoutResponse, error) {
	err := a.svc.Logout(ctx, req.GetPat())
	if err != nil {
		if status.Code(err) == codes.Internal {
			a.log.Error("failed to logout user", zap.Error(err))
		} else {
			a.log.Warn("failed to logout user", zap.Error(err))
		}
		return nil, err
	}

	a.log.Info("user logged out successfully", zap.String("token", req.GetPat()))
	return &pb.LogoutResponse{}, nil
}

// Validate validates the token.
func (a *Auth) Validate(ctx context.Context, req *pb.ValidateRequest) (*pb.ValidateResponse, error) {
	err := a.svc.Validate(ctx, req.GetPat())
	if err != nil {
		if status.Code(err) == codes.Internal {
			a.log.Error("failed to validate token", zap.Error(err))
		} else {
			a.log.Warn("failed to validate token", zap.Error(err))
		}
		return nil, err
	}

	a.log.Info("token validated successfully", zap.String("token", req.GetPat()))
	return &pb.ValidateResponse{}, nil
}
