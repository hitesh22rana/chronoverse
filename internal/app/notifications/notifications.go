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
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"

	notificationsmodel "github.com/hitesh22rana/chronoverse/internal/model/notifications"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
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
	logger *zap.Logger
	tp     trace.Tracer
	auth   auth.IAuth
	cfg    *Config
	svc    Service
}

// audienceInterceptor sets the audience from the metadata and adds it to the context.
func audienceInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Extract the audience from metadata.
		audience, err := auth.ExtractAudienceFromMetadata(ctx)
		if err != nil {
			return "", err
		}

		return handler(auth.WithAudience(ctx, audience), req)
	}
}

// roleInterceptor extracts the role from the metadata and adds it to the context.
func roleInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Extract the role from metadata.
		role, err := auth.ExtractRoleFromMetadata(ctx)
		if err != nil {
			return "", err
		}

		// Validate the role for internal APIs.
		if isInternalAPI(info.FullMethod) && role != auth.RoleAdmin.String() {
			return "", status.Error(codes.PermissionDenied, "unauthorized access")
		}

		return handler(auth.WithRole(ctx, role), req)
	}
}

// authTokenInterceptor extracts the authToken from metadata and adds it to the context.
func (n *Notifications) authTokenInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Extract the authToken from metadata.
		authToken, err := auth.ExtractAuthorizationTokenFromMetadata(ctx)
		if err != nil {
			return "", err
		}

		ctx = auth.WithAuthorizationToken(ctx, authToken)
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
func New(ctx context.Context, cfg *Config, auth auth.IAuth, svc Service) *grpc.Server {
	notifications := &Notifications{
		logger: loggerpkg.FromContext(ctx),
		tp:     otel.Tracer(svcpkg.Info().GetName()),
		auth:   auth,
		cfg:    cfg,
		svc:    svc,
	}

	server := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(
			audienceInterceptor(),
			roleInterceptor(),
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
		n.logger.Error(
			"failed to create notification",
			zap.Any("ctx", ctx),
			zap.Error(err),
		)
		return nil, err
	}

	n.logger.Info(
		"notification created successfully",
		zap.Any("ctx", ctx),
		zap.String("notification_id", notificationID),
	)
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
		n.logger.Error(
			"failed to mark all notifications as read",
			zap.Any("ctx", ctx),
			zap.Error(err),
		)
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
			attribute.String("kind", req.GetKind()),
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

	n.logger.Info(
		"all notifications fetched successfully",
		zap.Any("ctx", ctx),
		zap.Any("notifications", notifications),
	)
	return notifications.ToProto(), nil
}
