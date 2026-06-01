package outboxrelay

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Config represents the outbox relay repository configuration.
type Config struct {
	BatchSize       int
	MaxAttempts     int
	RetryBackoff    time.Duration
	ProcessingLease time.Duration
	WorkerID        string
}

// Event is a claimed outbox event ready to publish.
type Event struct {
	ID       string
	Topic    string
	KafkaKey string
	EventKey string
	Payload  []byte
	Attempts int
	Claim    string
}

// Repository publishes outbox events to Kafka.
type Repository struct {
	tp  trace.Tracer
	cfg *Config
	pg  *postgres.Postgres
	kfk *kgo.Client
}

// New creates a new outbox relay repository.
func New(cfg *Config, pg *postgres.Postgres, kfk *kgo.Client) *Repository {
	return &Repository{
		tp:  otel.Tracer(svcpkg.Info().GetName()),
		cfg: cfg,
		pg:  pg,
		kfk: kfk,
	}
}

// PublishTopic claims pending events for a topic and publishes them to Kafka.
func (r *Repository) PublishTopic(ctx context.Context, topic string, logger *zap.Logger) (int, error) {
	events, err := r.claim(ctx, topic)
	if err != nil {
		return 0, err
	}

	for _, event := range events {
		if err := r.publishEvent(ctx, event, logger); err != nil {
			return len(events), err
		}
	}

	return len(events), nil
}

func (r *Repository) publishEvent(ctx context.Context, event *Event, logger *zap.Logger) error {
	ctxWithTrace, span := r.tp.Start(
		ctx,
		"outboxrelay.PublishTopic.publishEvent",
		trace.WithAttributes(
			attribute.String("event_id", event.ID),
			attribute.String("topic", event.Topic),
			attribute.String("event_key", event.EventKey),
			attribute.String("kafka_key", event.KafkaKey),
			attribute.String("worker_id", r.cfg.WorkerID),
			attribute.Int("attempts", event.Attempts),
		),
	)
	defer span.End()

	record := &kgo.Record{
		Topic: event.Topic,
		Key:   []byte(event.KafkaKey),
		Value: event.Payload,
	}

	if err := r.kfk.ProduceSync(ctxWithTrace, record).FirstErr(); err != nil {
		logger.Error("failed to publish outbox event",
			zap.String("event_id", event.ID),
			zap.String("topic", event.Topic),
			zap.String("event_key", event.EventKey),
			zap.Error(err),
		)
		return r.markFailed(ctxWithTrace, event)
	}

	return r.markPublished(ctxWithTrace, event)
}

func (r *Repository) claim(ctx context.Context, topic string) ([]*Event, error) {
	claimToken, err := newClaimToken(r.cfg.WorkerID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create outbox claim token: %v", err)
	}

	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to start outbox transaction: %v", err)
	}
	//nolint:errcheck // Rollback is a no-op after commit.
	defer tx.Rollback(ctx)

	query := fmt.Sprintf(`
        WITH picked AS (
            SELECT e.id
            FROM %s AS e
            WHERE e.topic = $1
                AND (
                    (e.status = 'PENDING' AND e.next_attempt_at <= (now() AT TIME ZONE 'utc'))
                    OR (e.status = 'FAILED' AND e.next_attempt_at <= (now() AT TIME ZONE 'utc'))
                    OR (e.status = 'PROCESSING' AND e.locked_at <= (now() AT TIME ZONE 'utc') - $4::interval)
                )
                AND NOT EXISTS (
                    SELECT 1
                    FROM %s AS earlier
                    WHERE earlier.topic = e.topic
                        AND earlier.kafka_key = e.kafka_key
                        AND earlier.status <> 'PUBLISHED'
                        AND (earlier.created_at, earlier.id) < (e.created_at, e.id)
                )
            ORDER BY e.created_at, e.id
            FOR UPDATE SKIP LOCKED
            LIMIT $2
        )
        UPDATE %s e
        SET status = 'PROCESSING',
            locked_at = now() AT TIME ZONE 'utc',
            locked_by = $3,
            attempts = attempts + 1
        FROM picked
        WHERE e.id = picked.id
        RETURNING e.id, e.topic, e.kafka_key, e.event_key, e.payload::text, e.attempts;
    `, postgres.TableOutboxEvents, postgres.TableOutboxEvents, postgres.TableOutboxEvents)

	rows, err := tx.Query(ctx, query, topic, r.cfg.BatchSize, claimToken, postgresInterval(r.processingLease()))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to claim outbox events: %v", err)
	}
	defer rows.Close()

	events := make([]*Event, 0, r.cfg.BatchSize)
	for rows.Next() {
		var event Event
		var payload string
		if err = rows.Scan(&event.ID, &event.Topic, &event.KafkaKey, &event.EventKey, &payload, &event.Attempts); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to scan outbox event: %v", err)
		}
		event.Payload = []byte(payload)
		event.Claim = claimToken
		events = append(events, &event)
	}
	if err = rows.Err(); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to iterate outbox events: %v", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to commit outbox claim: %v", err)
	}

	return events, nil
}

func (r *Repository) markPublished(ctx context.Context, event *Event) error {
	query := fmt.Sprintf(`
        UPDATE %s
        SET status = 'PUBLISHED',
            published_at = now() AT TIME ZONE 'utc',
            locked_at = NULL,
            locked_by = NULL
        WHERE id = $1
            AND status = 'PROCESSING'
            AND locked_by = $2;
    `, postgres.TableOutboxEvents)
	ct, err := r.pg.Exec(ctx, query, event.ID, event.Claim)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to mark outbox event published: %v", err)
	}
	if ct.RowsAffected() == 0 {
		return status.Errorf(codes.Aborted, "outbox event %s is no longer held by claim", event.ID)
	}
	return nil
}

func (r *Repository) markFailed(ctx context.Context, event *Event) error {
	statusValue := "FAILED"
	if event.Attempts >= r.cfg.MaxAttempts {
		statusValue = "DEAD"
	}

	query := fmt.Sprintf(`
        UPDATE %s
        SET status = $2,
            next_attempt_at = (now() AT TIME ZONE 'utc') + $3::interval,
            locked_at = NULL,
            locked_by = NULL
        WHERE id = $1
            AND status = 'PROCESSING'
            AND locked_by = $4;
    `, postgres.TableOutboxEvents)
	ct, err := r.pg.Exec(ctx, query, event.ID, statusValue, postgresInterval(r.retryBackoff()), event.Claim)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to mark outbox event failed: %v", err)
	}
	if ct.RowsAffected() == 0 {
		return status.Errorf(codes.Aborted, "outbox event %s is no longer held by claim", event.ID)
	}
	return nil
}

func (r *Repository) retryBackoff() time.Duration {
	if r.cfg.RetryBackoff <= 0 {
		return 5 * time.Second
	}

	return r.cfg.RetryBackoff
}

func (r *Repository) processingLease() time.Duration {
	if r.cfg.ProcessingLease <= 0 {
		return 30 * time.Second
	}

	return r.cfg.ProcessingLease
}

func postgresInterval(duration time.Duration) string {
	if duration <= 0 {
		duration = time.Millisecond
	}

	return fmt.Sprintf("%d milliseconds", duration.Milliseconds())
}

func newClaimToken(workerID string) (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}

	return fmt.Sprintf("%s:%d:%s", workerID, time.Now().UnixNano(), hex.EncodeToString(token[:])), nil
}
