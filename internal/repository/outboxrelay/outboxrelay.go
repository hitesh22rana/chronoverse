package outboxrelay

import (
	"context"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

// Config represents the outbox relay repository configuration.
type Config struct {
	BatchSize    int
	MaxAttempts  int
	RetryBackoff time.Duration
	WorkerID     string
}

// Event is a claimed outbox event ready to publish.
type Event struct {
	ID       string
	Topic    string
	KafkaKey string
	EventKey string
	Payload  []byte
	Attempts int
}

// Repository publishes outbox events to Kafka.
type Repository struct {
	cfg *Config
	pg  *postgres.Postgres
	kfk *kgo.Client
}

// New creates a new outbox relay repository.
func New(cfg *Config, pg *postgres.Postgres, kfk *kgo.Client) *Repository {
	return &Repository{cfg: cfg, pg: pg, kfk: kfk}
}

// PublishTopic claims pending events for a topic and publishes them to Kafka.
func (r *Repository) PublishTopic(ctx context.Context, topic string, logger *zap.Logger) (int, error) {
	events, err := r.claim(ctx, topic)
	if err != nil {
		return 0, err
	}

	for _, event := range events {
		record := &kgo.Record{
			Topic: event.Topic,
			Key:   []byte(event.KafkaKey),
			Value: event.Payload,
		}

		if err := r.kfk.ProduceSync(ctx, record).FirstErr(); err != nil {
			logger.Error("failed to publish outbox event",
				zap.String("event_id", event.ID),
				zap.String("topic", event.Topic),
				zap.String("event_key", event.EventKey),
				zap.Error(err),
			)
			if markErr := r.markFailed(ctx, event); markErr != nil {
				return len(events), markErr
			}
			continue
		}

		if err := r.markPublished(ctx, event.ID); err != nil {
			return len(events), err
		}
	}

	return len(events), nil
}

func (r *Repository) claim(ctx context.Context, topic string) ([]*Event, error) {
	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to start outbox transaction: %v", err)
	}
	//nolint:errcheck // Rollback is a no-op after commit.
	defer tx.Rollback(ctx)

	query := fmt.Sprintf(`
		WITH picked AS (
			SELECT id
			FROM %s
			WHERE topic = $1
				AND (
					(status = 'PENDING' AND next_attempt_at <= (now() AT TIME ZONE 'utc'))
					OR (status = 'FAILED' AND next_attempt_at <= (now() AT TIME ZONE 'utc'))
					OR (status = 'PROCESSING' AND locked_at <= (now() AT TIME ZONE 'utc') - $4::interval)
				)
			ORDER BY created_at
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
	`, postgres.TableOutboxEvents, postgres.TableOutboxEvents)

	rows, err := tx.Query(ctx, query, topic, r.cfg.BatchSize, r.cfg.WorkerID, fmt.Sprintf("%d seconds", int(r.cfg.RetryBackoff.Seconds())))
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

func (r *Repository) markPublished(ctx context.Context, eventID string) error {
	query := fmt.Sprintf(`
		UPDATE %s
		SET status = 'PUBLISHED',
			published_at = now() AT TIME ZONE 'utc',
			locked_at = NULL,
			locked_by = NULL
		WHERE id = $1;
	`, postgres.TableOutboxEvents)
	if _, err := r.pg.Exec(ctx, query, eventID); err != nil {
		return status.Errorf(codes.Internal, "failed to mark outbox event published: %v", err)
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
		WHERE id = $1;
	`, postgres.TableOutboxEvents)
	if _, err := r.pg.Exec(ctx, query, event.ID, statusValue, fmt.Sprintf("%d seconds", int(r.cfg.RetryBackoff.Seconds()))); err != nil {
		return status.Errorf(codes.Internal, "failed to mark outbox event failed: %v", err)
	}
	return nil
}
