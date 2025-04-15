//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package notifications

import (
	"context"
	"encoding/base64"
	"encoding/json"

	"github.com/go-playground/validator/v10"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	notificationsmodel "github.com/hitesh22rana/chronoverse/internal/model/notifications"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"
)

// Repository provides notification related operations.
type Repository interface {
	CreateNotification(ctx context.Context, userID, kind, payload string) (string, error)
	MarkNotificationsRead(ctx context.Context, notificationIDs []string, userID string) error
	ListNotifications(ctx context.Context, userID, kind, cursor string) (*notificationsmodel.ListNotificationsResponse, error)
}

// Service provides notification related operations.
type Service struct {
	validator *validator.Validate
	tp        trace.Tracer
	repo      Repository
}

// New creates a new notifications service.
func New(validator *validator.Validate, repo Repository) *Service {
	return &Service{
		validator: validator,
		tp:        otel.Tracer(svcpkg.Info().GetName()),
		repo:      repo,
	}
}

// CreateNotificationRequest holds the request parameters for creating a notification.
type CreateNotificationRequest struct {
	UserID  string `validate:"required"`
	Kind    string `validate:"required"`
	Payload string `validate:"required"`
}

// CreateNotification creates a new notification.
func (s *Service) CreateNotification(ctx context.Context, req *notificationspb.CreateNotificationRequest) (notificationID string, err error) {
	ctx, span := s.tp.Start(ctx, "Service.CreateNotification")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&CreateNotificationRequest{
		UserID:  req.GetUserId(),
		Kind:    req.GetKind(),
		Payload: req.GetPayload(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return "", status.Error(codes.InvalidArgument, err.Error())
	}

	// Validate the JSON payload
	var _payload map[string]any
	if err = json.Unmarshal([]byte(req.GetPayload()), &_payload); err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid payload: %v", err)
		return "", err
	}

	// Create the notification
	notificationID, err = s.repo.CreateNotification(
		ctx,
		req.GetUserId(),
		req.GetKind(),
		req.GetPayload(),
	)
	if err != nil {
		return "", err
	}

	return notificationID, nil
}

// MarkNotificationsRead holds the request parameters for marking all notifications as read.
type MarkNotificationsRead struct {
	IDs    []string `validate:"required,min=1"`
	UserID string   `validate:"required"`
}

// MarkNotificationsRead marks all notifications as read.
func (s *Service) MarkNotificationsRead(ctx context.Context, req *notificationspb.MarkNotificationsReadRequest) (err error) {
	ctx, span := s.tp.Start(ctx, "Service.MarkNotificationsRead")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&MarkNotificationsRead{
		IDs:    req.GetIds(),
		UserID: req.GetUserId(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return err
	}

	// Mark all notifications as read
	err = s.repo.MarkNotificationsRead(ctx, req.GetIds(), req.GetUserId())
	return err
}

// ListNotificationsRequest holds the request parameters for listing notifications.
type ListNotificationsRequest struct {
	UserID string `validate:"required"`
	Kind   string `validate:"omitempty"`
	Cursor string `validate:"omitempty"`
}

// ListNotifications returns a list of notifications.
func (s *Service) ListNotifications(
	ctx context.Context,
	req *notificationspb.ListNotificationsRequest,
) (res *notificationsmodel.ListNotificationsResponse, err error) {
	ctx, span := s.tp.Start(ctx, "Service.ListNotifications")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&ListNotificationsRequest{
		UserID: req.GetUserId(),
		Kind:   req.GetKind(),
		Cursor: req.GetCursor(),
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Validate the cursor
	var cursor string
	if req.GetCursor() != "" {
		cursor, err = decodeCursor(req.GetCursor())
		if err != nil {
			err = status.Errorf(codes.InvalidArgument, "invalid cursor: %v", err)
			return nil, err
		}
	}

	// List the notifications
	res, err = s.repo.ListNotifications(ctx, req.GetUserId(), req.GetKind(), cursor)
	if err != nil {
		return nil, err
	}

	return res, nil
}

func decodeCursor(token string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return "", err
	}

	return string(decoded), nil
}
