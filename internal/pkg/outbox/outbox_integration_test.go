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
	require.NoError(t, tx.QueryRow(t.Context(), `
		SELECT
			count(*),
			min(payload::text)
		FROM
			outbox_events
		WHERE
			topic = $1
			AND event_key = $2
	`, event.Topic, key).Scan(&count, &payload))
	require.Equal(t, 1, count)
	require.JSONEq(t, `{"id":"resource"}`, payload)
	require.NoError(t, tx.Rollback(t.Context()))
	require.NoError(t, pg.QueryRow(t.Context(), `SELECT count(*) FROM outbox_events WHERE topic=$1 AND event_key=$2`, event.Topic, key).Scan(&count))
	require.Zero(t, count)
}

func TestReplayIntervalContract(t *testing.T) {
	for _, tc := range []struct {
		name                                 string
		base, maximum, wantBase, wantMaximum time.Duration
		invalid                              bool
	}{
		{name: "defaults", wantBase: 5 * time.Minute, wantMaximum: time.Hour},
		{name: "minimum", base: time.Millisecond, maximum: time.Millisecond, wantBase: time.Millisecond, wantMaximum: time.Millisecond},
		{name: "default maximum", base: time.Second, wantBase: time.Second, wantMaximum: time.Hour},
		{name: "default base", maximum: 2 * time.Hour, wantBase: 5 * time.Minute, wantMaximum: 2 * time.Hour},
		{name: "negative base", base: -1, invalid: true},
		{name: "negative maximum", maximum: -1, invalid: true},
		{name: "submillisecond", base: time.Microsecond, invalid: true},
		{name: "fractional base", base: time.Millisecond + 1, invalid: true},
		{name: "fractional maximum", maximum: time.Hour + 1, invalid: true},
		{name: "inverted", base: 2 * time.Hour, maximum: time.Hour, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, maximum, err := outbox.NormalizeReplayIntervals(tc.base, tc.maximum)
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid replay timing accepted")
				}
				return
			}
			if err != nil || base != tc.wantBase || maximum != tc.wantMaximum {
				t.Fatalf("got %s/%s/%v want %s/%s", base, maximum, err, tc.wantBase, tc.wantMaximum)
			}
		})
	}
}
