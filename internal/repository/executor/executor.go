package executor

import (
	"context"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Services represents the services used by the executor.
type Services struct {
	Jobs jobspb.JobsServiceClient
}

// Repository provides executor repository.
type Repository struct {
	tp   trace.Tracer
	kfk  *kgo.Client
	auth *auth.Auth
	svc  *Services
}

// New creates a new executor repository.
func New(auth *auth.Auth, svc *Services, kfk *kgo.Client) *Repository {
	return &Repository{
		tp:   otel.Tracer(svcpkg.Info().GetName()),
		auth: auth,
		svc:  svc,
		kfk:  kfk,
	}
}

// Run starts the executor.
func (r *Repository) Run(ctx context.Context) (err error) {
	logger := loggerpkg.FromContext(ctx)
loop:
	for {
		fetches := r.kfk.PollFetches(ctx)
		iter := fetches.RecordIter()

		for _, fetchErr := range fetches.Errors() {
			logger.Error("error consuming from topic=%s partition=%d error=%v",
				zap.String("topic", fetchErr.Topic),
				zap.Int32("partition", fetchErr.Partition),
				zap.Error(fetchErr.Err),
			)
			break loop
		}

		for !iter.Done() {
			record := iter.Next()
			logger.Info("consumed record from partition %d with message: %s",
				zap.Int32("partition", record.Partition),
				zap.String("message", string(record.Value)),
			)
		}
	}

	return nil
}
