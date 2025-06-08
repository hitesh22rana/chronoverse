//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package notifications

import (
	"context"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"

	notificationsmodel "github.com/hitesh22rana/chronoverse/internal/model/notifications"
	authpkg "github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	grpcmiddlewares "github.com/hitesh22rana/chronoverse/internal/pkg/grpc/middlewares"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// internalAPIs contains the list of internal APIs that require admin role.
// These APIs are not exposed to the public and should only be used internally.
var internalAPIs = map[string]bool{
	"CreateNotification": true,
}

// Service provides notification related operations.
type Service interface {
	CreateNotification(ctx context.Context, req *notificationspb.CreateNotificationRequest) (string, error)
	MarkNotificationsRead(ctx context.Context, req *notificationspb.MarkNotificationsReadRequest) error
	ListNotifications(ctx context.Context, req *notificationspb.ListNotificationsRequest) (*notificationsmodel.ListNotificationsResponse, error)
}

// Config represents the notifications service configuration.
type Config struct {
	Deadline    time.Duration
	Environment string
}

// Notifications represents the notifications service.
type Notifications struct {
	notificationspb.UnimplementedNotificationsServiceServer
	tp   trace.Tracer
	auth authpkg.IAuth
	cfg  *Config
	svc  Service
}

// authTokenInterceptor extracts and validates the authToken from the metadata and adds it to the context.
func (n *Notifications) authTokenInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Extract the authToken from metadata.
		authToken, err := authpkg.ExtractAuthorizationTokenFromMetadata(ctx)
		if err != nil {
			return "", err
		}

		ctx = authpkg.WithAuthorizationToken(ctx, authToken)
		if _, err := n.auth.ValidateToken(ctx); err != nil {
			return "", err
		}

		return handler(ctx, req)
	}
}

// isInternalAPI checks if the full method is an internal API.
func isInternalAPI(fullMethod string) bool {
	parts := strings.Split(fullMethod, "/")
	if len(parts) < 3 {
		return false
	}

	return internalAPIs[parts[2]]
}

// isProduction checks if the environment is production.
func isProduction(environment string) bool {
	return strings.EqualFold(environment, "production")
}

// New creates a new notifications server.
func New(ctx context.Context, cfg *Config, auth authpkg.IAuth, svc Service) *grpc.Server {
	notifications := &Notifications{
		tp:   otel.Tracer(svcpkg.Info().GetName()),
		auth: auth,
		cfg:  cfg,
		svc:  svc,
	}

	server := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(
			grpcmiddlewares.LoggingInterceptor(loggerpkg.FromContext(ctx)),
			grpcmiddlewares.AudienceInterceptor(),
			grpcmiddlewares.RoleInterceptor(func(method, role string) bool {
				return isInternalAPI(method) && role != authpkg.RoleAdmin.String()
			}),
			notifications.authTokenInterceptor(),
		),
	)
	notificationspb.RegisterNotificationsServiceServer(server, notifications)

	// Only register reflection for non-production environments.
	if !isProduction(cfg.Environment) {
		reflection.Register(server)
	}
	return server
}

// CreateNotification creates a new notification.
// This is an internal method used by internal services, and it should not be exposed to the public.
func (n *Notifications) CreateNotification(
	ctx context.Context,
	req *notificationspb.CreateNotificationRequest,
) (res *notificationspb.CreateNotificationResponse, err error) {
	ctx, span := n.tp.Start(
		ctx,
		"App.CreateNotification",
		trace.WithAttributes(
			attribute.String("user_id", req.GetUserId()),
			attribute.String("kind", req.GetKind()),
			attribute.String("payload", req.GetPayload()),
		),
	)
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	ctx, cancel := context.WithTimeout(ctx, n.cfg.Deadline)
	defer cancel()

	notificationID, err := n.svc.CreateNotification(ctx, req)
	if err != nil {
		return nil, err
	}

	return &notificationspb.CreateNotificationResponse{Id: notificationID}, nil
}

// MarkNotificationsRead marks all notifications as read.
func (n *Notifications) MarkNotificationsRead(
	ctx context.Context,
	req *notificationspb.MarkNotificationsReadRequest,
) (res *notificationspb.MarkNotificationsReadResponse, err error) {
	ctx, span := n.tp.Start(
		ctx,
		"App.MarkNotificationsRead",
		trace.WithAttributes(
			attribute.StringSlice("ids", req.GetIds()),
			attribute.String("user_id", req.GetUserId()),
		),
	)
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	ctx, cancel := context.WithTimeout(ctx, n.cfg.Deadline)
	defer cancel()

	err = n.svc.MarkNotificationsRead(ctx, req)
	if err != nil {
		return nil, err
	}

	return &notificationspb.MarkNotificationsReadResponse{}, nil
}

// ListNotifications returns a list of notifications.
func (n *Notifications) ListNotifications(
	ctx context.Context,
	req *notificationspb.ListNotificationsRequest,
) (res *notificationspb.ListNotificationsResponse, err error) {
	ctx, span := n.tp.Start(
		ctx,
		"App.ListNotifications",
		trace.WithAttributes(
			attribute.String("user_id", req.GetUserId()),
			attribute.String("cursor", req.GetCursor()),
		),
	)
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	ctx, cancel := context.WithTimeout(ctx, n.cfg.Deadline)
	defer cancel()

	notifications, err := n.svc.ListNotifications(ctx, req)
	if err != nil {
		span.SetStatus(otelcodes.Error, err.Error())
		span.RecordError(err)
		return nil, err
	}

	return notifications.ToProto(), nil
}
