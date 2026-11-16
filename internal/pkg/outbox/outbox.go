package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

// Event is a Kafka publish intent stored in the Postgres outbox.
type Event struct {
	Topic    string
	KafkaKey string
	EventKey string
	Payload  any
}

// InsertTx inserts an outbox event in the caller's transaction.
func InsertTx(ctx context.Context, tx pgx.Tx, event *Event) error {
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to marshal outbox payload: %v", err)
	}

	query := fmt.Sprintf(`
		INSERT INTO %s (topic, kafka_key, event_key, payload)
		VALUES ($1, $2, $3, $4::jsonb)
		ON CONFLICT (topic, event_key) DO NOTHING;
	`, postgres.TableOutboxEvents)

	if _, err = tx.Exec(
		ctx,
		query,
		event.Topic,
		event.KafkaKey,
		event.EventKey,
		string(payload),
	); err != nil {
		return status.Errorf(codes.Internal, "failed to insert outbox event: %v", err)
	}

	return nil
}

// NormalizeReplayIntervals shares omission and precision rules across env and repository callers.
func NormalizeReplayIntervals(interval, maximum time.Duration) (normalizedInterval, normalizedMaximum time.Duration, err error) {
	if interval == 0 {
		interval = 5 * time.Minute
	}
	if maximum == 0 {
		maximum = time.Hour
	}
	if interval < time.Millisecond || maximum < interval || interval%time.Millisecond != 0 || maximum%time.Millisecond != 0 {
		return 0, 0, errors.New("outbox replay intervals require whole milliseconds and 1ms <= interval <= maximum")
	}
	return interval, maximum, nil
}
