//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package scheduler

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Repository provides scheduler related operations.
type Repository interface {
	Run(ctx context.Context) (int, error)
}

// Service provides scheduler related operations.
type Service struct {
	tp   trace.Tracer
	repo Repository
}

// New creates a new scheduler service.
func New(repo Repository) *Service {
	return &Service{
		tp:   otel.Tracer(svcpkg.Info().GetName()),
		repo: repo,
	}
}

// Run starts the scheduler.
func (s *Service) Run(ctx context.Context) (total int, err error) {
	ctx, span := s.tp.Start(ctx, "Service.Run")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	total, err = s.repo.Run(ctx)
	if err != nil {
		return 0, err
	}

	span.AddEvent("scheduled jobs", trace.WithAttributes(
		attribute.KeyValue{
			Key:   attribute.Key("total"),
			Value: attribute.IntValue(total),
		},
	))
	return total, nil
}
