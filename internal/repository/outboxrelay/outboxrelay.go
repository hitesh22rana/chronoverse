package outboxrelay

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/outbox"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Config represents the outbox relay repository configuration.
type Config struct {
	BatchSize         int
	MaxAttempts       int
	RetryBackoff      time.Duration
	ProcessingLease   time.Duration
	WorkerID          string
	ReplayInterval    time.Duration
	ReplayMaxInterval time.Duration
}

// Event is a claimed outbox event ready to publish.
type Event struct {
	ID          string
	Topic       string
	KafkaKey    string
	EventKey    string
	Payload     []byte
	Attempts    int
	Claim       string
	ReplayRound int
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
	normalized := *cfg
	if normalized.BatchSize == 0 {
		normalized.BatchSize = 100
	}
	if normalized.MaxAttempts == 0 {
		normalized.MaxAttempts = 10
	}
	if normalized.RetryBackoff == 0 {
		normalized.RetryBackoff = 5 * time.Second
	}
	if normalized.ProcessingLease == 0 {
		normalized.ProcessingLease = 30 * time.Second
	}
	return &Repository{
		tp:  otel.Tracer(svcpkg.Info().GetName()),
		cfg: &normalized,
		pg:  pg,
		kfk: kfk,
	}
}

// PublishTopic claims pending events for a topic and publishes them to Kafka.
func (r *Repository) PublishTopic(ctx context.Context, topic string, logger *zap.Logger) (int, error) {
	if topic == kafka.TopicWorkflows {
		if _, _, configErr := outbox.NormalizeReplayIntervals(r.cfg.ReplayInterval, r.cfg.ReplayMaxInterval); configErr != nil {
			return 0, status.Error(codes.InvalidArgument, configErr.Error())
		}
	}
	if r.cfg.BatchSize <= 0 || r.cfg.MaxAttempts <= 0 || r.cfg.RetryBackoff < time.Millisecond || r.cfg.ProcessingLease < time.Millisecond {
		return 0, status.Error(codes.InvalidArgument, "outbox batch/attempt limits must be positive and transport timing at least one millisecond")
	}
	if err := r.requeueUnfinishedCleanup(ctx, topic); err != nil {
		return 0, err
	}
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
	claimToken := newClaimToken(r.cfg.WorkerID)

	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to start outbox transaction: %v", err)
	}
	//nolint:errcheck // Rollback is a no-op after commit.
	defer tx.Rollback(ctx)

	var events []*Event
	if topic == kafka.TopicWorkflows {
		events, err = r.claimWorkflow(ctx, tx, topic, claimToken)
	} else {
		events, err = r.claimBatch(ctx, tx, topic, claimToken, r.cfg.BatchSize, nil, []string{})
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to commit outbox claim: %v", err)
	}
	return events, nil
}

func (r *Repository) claimBatch(
	ctx context.Context,
	tx pgx.Tx,
	topic,
	claimToken string,
	limit int,
	replay *bool,
	excluded []string,
) (
	[]*Event,
	error,
) {
	query := fmt.Sprintf(`
		WITH
			picked AS (
				SELECT
					e.id,
					e.created_at
				FROM
					%s AS e
				WHERE
					e.topic = $1
					AND (
						(
							e.status = 'PENDING'
							AND e.next_attempt_at <= (now() AT TIME ZONE 'utc')
						)
						OR (
							e.status = 'FAILED'
							AND e.next_attempt_at <= (now() AT TIME ZONE 'utc')
						)
						OR (
							e.status = 'PROCESSING'
							AND e.locked_at <= (now() AT TIME ZONE 'utc') - $4::interval
						)
					)
					AND (
						$5::boolean IS NULL
						OR (
							e.cleanup_state IS NOT NULL
							AND e.cleanup_replay_round > 0
						) = $5
					)
					AND NOT (e.id = ANY ($6::uuid[]))
					AND NOT EXISTS (
						SELECT
							1
						FROM
							%s AS earlier
						WHERE
							earlier.topic = e.topic
							AND earlier.kafka_key = e.kafka_key
							AND earlier.status <> 'PUBLISHED'
							AND (earlier.created_at, earlier.id) < (e.created_at, e.id)
					)
				ORDER BY
					e.created_at,
					e.id
				FOR UPDATE
					SKIP LOCKED
				LIMIT
					$2
			),
			claimed AS (
				UPDATE %s e
				SET
					status = 'PROCESSING',
					locked_at = statement_timestamp() AT TIME ZONE 'utc',
					locked_by = $3,
					attempts = attempts + 1
				FROM
					picked
				WHERE
					e.id = picked.id
				RETURNING
					e.id,
					e.topic,
					e.kafka_key,
					e.event_key,
					e.payload::text,
					e.attempts,
					e.cleanup_replay_round
			)
		SELECT
			claimed.*
		FROM
			claimed
			JOIN picked USING (id)
		ORDER BY
			picked.created_at,
			picked.id;
	`, postgres.TableOutboxEvents, postgres.TableOutboxEvents, postgres.TableOutboxEvents)

	rows, err := tx.Query(ctx, query, topic, limit, claimToken, postgresInterval(r.cfg.ProcessingLease), replay, excluded)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to claim outbox events: %v", err)
	}
	defer rows.Close()
	events := make([]*Event, 0, limit)
	for rows.Next() {
		var event Event
		var payload string
		if err = rows.Scan(&event.ID, &event.Topic, &event.KafkaKey, &event.EventKey, &payload, &event.Attempts, &event.ReplayRound); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to scan outbox event: %v", err)
		}
		event.Payload = []byte(payload)
		event.Claim = claimToken
		events = append(events, &event)
	}
	if err = rows.Err(); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to iterate outbox events: %v", err)
	}
	return events, nil
}

func (r *Repository) markPublished(ctx context.Context, event *Event) error {
	delay := time.Duration(0)
	if event.Topic == kafka.TopicWorkflows {
		base, maximum, err := outbox.NormalizeReplayIntervals(r.cfg.ReplayInterval, r.cfg.ReplayMaxInterval)
		if err != nil {
			return status.Error(codes.InvalidArgument, err.Error())
		}
		delay = replayDelay(base, maximum, event.ReplayRound)
	}
	query := fmt.Sprintf(`
		UPDATE %s
		SET
			status = 'PUBLISHED',
			published_at = now() AT TIME ZONE 'utc',
			cleanup_replay_due_at = CASE
				WHEN cleanup_state IN ('READY', 'RETRY', 'PROCESSING') THEN
					(now() AT TIME ZONE 'utc') + $3::interval
				ELSE NULL
			END,
			locked_at = NULL,
			locked_by = NULL
		WHERE
			id = $1
			AND status = 'PROCESSING'
			AND locked_by = $2;
	`, postgres.TableOutboxEvents)
	ct, err := r.pg.Exec(ctx, query, event.ID, event.Claim, postgresInterval(delay))
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
		SET
			status = $2,
			next_attempt_at = (now() AT TIME ZONE 'utc') + $3::interval,
			locked_at = NULL,
			locked_by = NULL
		WHERE
			id = $1
			AND status = 'PROCESSING'
			AND locked_by = $4;
	`, postgres.TableOutboxEvents)
	ct, err := r.pg.Exec(ctx, query, event.ID, statusValue, postgresInterval(r.cfg.RetryBackoff), event.Claim)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to mark outbox event failed: %v", err)
	}
	if ct.RowsAffected() == 0 {
		return status.Errorf(codes.Aborted, "outbox event %s is no longer held by claim", event.ID)
	}
	return nil
}

func postgresInterval(duration time.Duration) string {
	if duration <= 0 {
		duration = time.Millisecond
	}

	return fmt.Sprintf("%d milliseconds", duration.Milliseconds())
}

// newClaimToken returns a unique claim token for workerID. crypto/rand.Read
// never fails: it fills the buffer or terminates the process on entropy loss.
func newClaimToken(workerID string) string {
	var token [16]byte
	rand.Read(token[:])

	return fmt.Sprintf("%s:%d:%s", workerID, time.Now().UnixNano(), hex.EncodeToString(token[:]))
}

func replayDelay(base, maximum time.Duration, round int) time.Duration {
	result := base
	for range round {
		if result > maximum/2 {
			return maximum
		}
		result *= 2
	}
	return result
}
