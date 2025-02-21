//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package jobs

import (
	"context"
	"encoding/json"

	"github.com/go-playground/validator/v10"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Repository provides job related operations.
type Repository interface {
	Create(ctx context.Context, userID, name, payload, kind string, interval, maxRetries int32) (string, error)
}

// Service provides job related operations.
type Service struct {
	validator *validator.Validate
	tp        trace.Tracer
	repo      Repository
}

// New creates a new jobs-service.
func New(validator *validator.Validate, repo Repository) *Service {
	return &Service{
		validator: validator,
		tp:        otel.Tracer(svc.Info().GetName()),
		repo:      repo,
	}
}

// CreateRequest holds the request parameters for creating a new job.
type CreateRequest struct {
	UserID     string `validate:"required"`
	Name       string `validate:"required"`
	Payload    string `validate:"required"`
	Kind       string `validate:"required"`
	Interval   int32  `validate:"required"`
	MaxRetries int32  `validate:"required"`
}

// Create a new job.
func (s *Service) Create(ctx context.Context, userID, name, payload, kind string, interval, maxRetries int32) (jobID string, err error) {
	ctx, span := s.tp.Start(ctx, "Service.Create")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Validate the request
	err = s.validator.Struct(&CreateRequest{
		UserID:     userID,
		Name:       name,
		Payload:    payload,
		Kind:       kind,
		Interval:   interval,
		MaxRetries: maxRetries,
	})
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
		return "", status.Error(codes.InvalidArgument, err.Error())
	}

	// Validate the JSON payload
	var _payload map[string]interface{}
	if err = json.Unmarshal([]byte(payload), &_payload); err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid payload: %v", err)
		return "", err
	}

	// Create the job
	jobID, err = s.repo.Create(ctx, userID, name, payload, kind, interval, maxRetries)
	if err != nil {
		return "", err
	}

	return jobID, nil
}
