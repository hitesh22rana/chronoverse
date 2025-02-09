//go:generate go run go.uber.org/mock/mockgen@v0.5.0 -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package auth

import (
	"context"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	pb "github.com/hitesh22rana/chronoverse/pkg/proto/go"

	patpkg "github.com/hitesh22rana/chronoverse/internal/pkg/pat"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Service provides user authentication operations.
type Service interface {
	Register(ctx context.Context, email, password string) (string, string, error)
	Login(ctx context.Context, email, password string) (string, string, error)
	Logout(ctx context.Context, token string) (string, error)
	Validate(ctx context.Context, token string) (string, error)
}

// Auth represents the authentication application.
type Auth struct {
	pb.UnimplementedAuthServiceServer
	log *zap.Logger
	tp  trace.Tracer
	svc Service
}

// audienceInterceptor sets the audience in the context.
func audienceInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Extract the audience from metadata.
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.InvalidArgument, "metadata is required")
		}

		audience := md.Get("audience")
		if len(audience) == 0 {
			return nil, status.Error(codes.InvalidArgument, "audience is required")
		}

		ctx = patpkg.WithAudience(ctx, audience[0])
		return handler(ctx, req)
	}
}

// New creates a new authentication server.
func New(log *zap.Logger, svc Service) *grpc.Server {
	serviceName := svcpkg.Info().GetName()
	server := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.UnaryInterceptor(audienceInterceptor()),
	)
	pb.RegisterAuthServiceServer(server, &Auth{
		log: log,
		tp:  otel.Tracer(serviceName),
		svc: svc,
	})

	reflection.Register(server)
	return server
}

// Register registers a new user.
func (a *Auth) Register(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
	ctx, span := a.tp.Start(ctx, "App.Register")
	defer span.End()

	userID, pat, err := a.svc.Register(ctx, req.GetEmail(), req.GetPassword())
	if err != nil {
		a.log.Error("failed to register user", zap.Error(err))
		return nil, err
	}

	a.log.Info("user registered successfully", zap.String("user_id", userID))
	return &pb.RegisterResponse{Pat: pat}, nil
}

// Login logs in the user.
func (a *Auth) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error) {
	userID, pat, err := a.svc.Login(ctx, req.GetEmail(), req.GetPassword())
	if err != nil {
		a.log.Error("failed to login user", zap.Error(err))
		return nil, err
	}

	a.log.Info("user logged in successfully", zap.String("user_id", userID))
	return &pb.LoginResponse{Pat: pat}, nil
}

// Logout logs out the user.
func (a *Auth) Logout(ctx context.Context, req *pb.LogoutRequest) (*pb.LogoutResponse, error) {
	userID, err := a.svc.Logout(ctx, req.GetPat())
	if err != nil {
		a.log.Error("failed to logout user", zap.Error(err))
		return nil, err
	}

	a.log.Info("user logged out successfully", zap.String("user_id", userID))
	return &pb.LogoutResponse{}, nil
}

// Validate validates the token.
func (a *Auth) Validate(ctx context.Context, req *pb.ValidateRequest) (*pb.ValidateResponse, error) {
	userID, err := a.svc.Validate(ctx, req.GetPat())
	if err != nil {
		a.log.Error("failed to validate token", zap.Error(err))
		return nil, err
	}

	a.log.Info("token validated successfully", zap.String("user_id", userID))
	return &pb.ValidateResponse{}, nil
}
