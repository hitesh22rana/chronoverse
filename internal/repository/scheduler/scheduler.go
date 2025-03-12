package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	delimiter          = '$'
	scheduledJobsTable = "scheduled_jobs"
)

// Config represents the repository constants configuration.
type Config struct {
	FetchLimit int
	BatchSize  int
}

// Repository provides scheduler repository.
type Repository struct {
	tp  trace.Tracer
	cfg *Config
	pg  *postgres.Postgres
	kfk *kgo.Client
}

// New creates a new scheduler repository.
func New(cfg *Config, pg *postgres.Postgres, kfk *kgo.Client) *Repository {
	return &Repository{
		tp:  otel.Tracer(svcpkg.Info().GetName()),
		cfg: cfg,
		pg:  pg,
		kfk: kfk,
	}
}

// Run starts the scheduler.
//
//nolint:gocyclo // The cyclomatic complexity is acceptable
func (r *Repository) Run(ctx context.Context) (total int, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.Run")
	defer func() {
		if err != nil {
			span.RecordError(err)
		}
		span.End()
	}()

	// Start transaction
	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		err = status.Errorf(codes.Internal, "failed to start transaction: %v", err)
		return 0, err
	}
	//nolint:errcheck // The error is handled in the next line
	defer tx.Rollback(ctx)

	query := fmt.Sprintf(`
		UPDATE %s
		SET status = 'QUEUED', updated_at = NOW()
		WHERE id IN (
			SELECT id
			FROM %s
			WHERE status = 'PENDING' AND scheduled_at <= NOW()
			FOR UPDATE SKIP LOCKED
			LIMIT %d
		)
		RETURNING id, job_id, scheduled_at;
	`, scheduledJobsTable, scheduledJobsTable, r.cfg.FetchLimit)

	// Execute query
	rows, err := tx.Query(ctx, query)
	if err != nil {
		err = status.Errorf(codes.Internal, "failed to query scheduled jobs: %v", err)
		return 0, err
	}
	defer rows.Close()

	// Iterate over the rows and collect the IDs
	//nolint:prealloc // We don't know the number of rows
	var records []*kgo.Record
	for rows.Next() {
		var id string
		var jobID string
		var scheduledAt time.Time
		if err = rows.Scan(&id, &jobID, &scheduledAt); err != nil {
			err = status.Errorf(codes.Internal, "failed to scan scheduled job: %v", err)
			return 0, err
		}
		record := fmt.Sprintf(
			"%s%c%s%c%s",
			id,
			delimiter,
			jobID,
			delimiter,
			scheduledAt.Format(time.RFC3339Nano),
		)
		records = append(records, kgo.StringRecord(record))
	}

	// Handle any errors that may have occurred during iteration
	if err = rows.Err(); err != nil {
		err = status.Errorf(codes.Internal, "failed to iterate over scheduled jobs: %v", err)
		return 0, err
	}

	// Divide the records into batches
	recordsBatch := batch(records, r.cfg.BatchSize)
	if len(recordsBatch) == 0 {
		return 0, nil
	}

	// Publish the data to Kafka
	for {
		// Begin the kafka transaction
		if err = r.kfk.BeginTransaction(); err != nil {
			err = status.Errorf(codes.Internal, "failed to begin kafka transaction: %v", err)
			return 0, err
		}

		// Publish the data to Kafka
		for _, batch := range recordsBatch {
			if err = r.kfk.ProduceSync(ctx, batch...).FirstErr(); err != nil {
				err = rollback(ctx, r.kfk)
				if err != nil {
					return 0, err
				}
			}
		}

		// Flush all the buffered messages
		// Flush only returns an error if the context was canceled, and we don't want to handle that error
		if _err := r.kfk.Flush(ctx); _err != nil {
			break // nothing to do here, since error means context was canceled
		}

		// Attempt to commit the transaction and explicitly abort if the commit was not attempted.
		//nolint:nestif // The nested if statements are necessary
		if err = r.kfk.EndTransaction(ctx, kgo.TryCommit); err != nil {
			if errors.Is(err, kerr.OperationNotAttempted) {
				err = rollback(ctx, r.kfk)
				if err != nil {
					return 0, err
				}
			} else {
				err = status.Errorf(codes.Internal, "failed to commit transaction: %v", err)
				return 0, err
			}
		} else {
			// Since the transaction was committed, we can break out of the loop
			break
		}
	}

	// Commit transaction
	if err = tx.Commit(ctx); err != nil {
		err = status.Errorf(codes.Internal, "failed to commit transaction: %v", err)
		return 0, err
	}

	total = len(records)
	return total, nil
}

func batch(data []*kgo.Record, size int) (batch [][]*kgo.Record) {
	if len(data) == 0 {
		return nil
	}

	for size < len(data) {
		data, batch = data[size:], append(batch, data[0:size:size])
	}
	return append(batch, data)
}

func rollback(ctx context.Context, kfk *kgo.Client) error {
	if err := kfk.AbortBufferedRecords(ctx); err != nil {
		return status.Errorf(codes.Canceled, "failed to abort buffered records: %v", err)
	}

	// Explicitly abort the transaction
	if err := kfk.EndTransaction(ctx, kgo.TryAbort); err != nil {
		return status.Errorf(codes.Internal, "failed to rollback transaction: %v", err)
	}

	return nil
}
