package outbox_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/outbox"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestMain(m *testing.M) { testkit.Run(m, testkit.WithPostgres()) }

func TestIntegrationOutboxValidationAndDedupStayInsideCallerTransaction(t *testing.T) {
	pg := testkit.Postgres(t)
	tx, err := pg.BeginTx(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			t.Errorf("rollback outbox transaction: %v", rollbackErr)
		}
	})
	key := "payload-boundary-" + uuid.NewString()
	event := &outbox.Event{Topic: "identity.contracts", KafkaKey: "resource", EventKey: key, Payload: math.Inf(1)}
	err = outbox.InsertTx(t.Context(), tx, event)
	require.Equal(t, codes.Internal, status.Code(err))
	require.Contains(t, err.Error(), "failed to marshal outbox payload")
	var count int
	require.NoError(t, tx.QueryRow(t.Context(), `SELECT count(*) FROM outbox_events WHERE topic=$1 AND event_key=$2`, event.Topic, key).Scan(&count))
	require.Zero(t, count)
	event.Payload = map[string]string{"id": "resource"}
	require.NoError(t, outbox.InsertTx(t.Context(), tx, event))
	event.Payload = map[string]string{"id": "duplicate must not overwrite"}
	require.NoError(t, outbox.InsertTx(t.Context(), tx, event))
	var payload string
	require.NoError(t, tx.QueryRow(t.Context(), `SELECT count(*),min(payload::text) FROM outbox_events WHERE topic=$1 AND event_key=$2`, event.Topic, key).Scan(&count, &payload))
	require.Equal(t, 1, count)
	require.JSONEq(t, `{"id":"resource"}`, payload)
	require.NoError(t, tx.Rollback(t.Context()))
	require.NoError(t, pg.QueryRow(t.Context(), `SELECT count(*) FROM outbox_events WHERE topic=$1 AND event_key=$2`, event.Topic, key).Scan(&count))
	require.Zero(t, count)
}
