//nolint:testpackage // Verifies transaction rollback at pgx driver boundaries.
package scheduler

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	otelcodes "go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestBatchSizeUsesDefaultUnlessPositive(t *testing.T) {
	for _, tc := range []struct {
		cfg  *Config
		want int
	}{{nil, 1000}, {&Config{}, 1000}, {&Config{BatchSize: -1}, 1000}, {&Config{BatchSize: 7}, 7}} {
		require.Equal(t, tc.want, New(tc.cfg, nil).batchSize())
	}
}

type interruptedStarter struct {
	pg    *postgres.Postgres
	stage string
}

func (s interruptedStarter) BeginTx(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.pg.BeginTx(ctx)
	if err != nil {
		return nil, err
	}
	return &interruptedTx{Tx: tx, stage: s.stage}, nil
}

type interruptedTx struct {
	pgx.Tx
	stage string
}

func (tx *interruptedTx) Query(ctx context.Context, q string, args ...any) (pgx.Rows, error) {
	if tx.stage == "query" {
		return tx.Tx.Query(ctx, "SELECT definitely_missing_scheduler_column")
	}
	rows, err := tx.Tx.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return &interruptedRows{Rows: rows, stage: tx.stage}, nil
}

func (tx *interruptedTx) Exec(ctx context.Context, q string, args ...any) (pgconn.CommandTag, error) {
	if tx.stage == "outbox" && strings.Contains(q, "INSERT INTO outbox_events") {
		return tx.Tx.Exec(ctx, "SELECT 1/0")
	}
	return tx.Tx.Exec(ctx, q, args...)
}

func (tx *interruptedTx) Commit(ctx context.Context) error {
	if tx.stage == "commit" {
		_, err := tx.Tx.Exec(ctx, `CREATE TEMP TABLE scheduler_commit_fault (id int UNIQUE DEFERRABLE INITIALLY DEFERRED); INSERT INTO scheduler_commit_fault VALUES(1),(1)`)
		if err != nil {
			return err
		}
	}
	return tx.Tx.Commit(ctx)
}

type interruptedRows struct {
	pgx.Rows
	stage string
}

func (r *interruptedRows) Scan(dest ...any) error {
	if r.stage == "scan" {
		return io.ErrUnexpectedEOF
	}
	return r.Rows.Scan(dest...)
}

func (r *interruptedRows) Err() error {
	if r.stage == "iteration" {
		return io.ErrUnexpectedEOF
	}
	return r.Rows.Err()
}

func TestIntegrationScheduleFailuresRollBackAndAllowRetry(t *testing.T) {
	for _, stage := range []string{"begin", "query", "scan", "iteration", "outbox", "commit"} {
		t.Run(stage, func(t *testing.T) {
			pg := testkit.Postgres(t)
			workflowID, jobID := seedUserWorkflowJob(t.Context(), t, pg, -time.Minute)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
				defer cancel()
				_, err := pg.Exec(ctx, "DELETE FROM workflows WHERE id=$1", workflowID)
				require.NoError(t, err)
			})
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
				defer cancel()
				require.NoError(t, provider.Shutdown(ctx))
			})
			repo := New(&Config{BatchSize: 1}, pg)
			repo.tp = provider.Tracer("scheduler-fault")
			repo.pg = interruptedStarter{pg: pg, stage: stage}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if stage == "begin" {
				cancel()
			}
			total, err := repo.Run(ctx)
			require.Zero(t, total)
			require.Equal(t, codes.Internal, status.Code(err))
			require.Contains(t, err.Error(), map[string]string{
				"begin": "start transaction", "query": "query jobs", "scan": "scan job",
				"iteration": "iterate over jobs", "outbox": "insert outbox event", "commit": "commit transaction",
			}[stage])
			spans := recorder.Ended()
			require.Len(t, spans, 1)
			require.Equal(t, otelcodes.Error, spans[0].Status().Code)
			require.Equal(t, err.Error(), spans[0].Status().Description)
			var state string
			var attempts int
			require.NoError(t, pg.QueryRow(t.Context(), "SELECT status,dispatch_attempts FROM jobs WHERE id=$1", jobID).Scan(&state, &attempts))
			require.Equal(t, "PENDING", state)
			require.Zero(t, attempts)
			var count int
			require.NoError(t, pg.QueryRow(t.Context(), "SELECT count(*) FROM outbox_events WHERE kafka_key=$1", workflowID).Scan(&count))
			require.Zero(t, count)
			repo.pg = pg
			total, err = repo.Run(t.Context())
			require.NoError(t, err)
			require.Equal(t, 1, total)
			require.NoError(t, pg.QueryRow(t.Context(), "SELECT status,dispatch_attempts FROM jobs WHERE id=$1", jobID).Scan(&state, &attempts))
			require.Equal(t, "QUEUED", state)
			require.Equal(t, 1, attempts)
			require.NoError(t, pg.QueryRow(t.Context(), "SELECT count(*) FROM outbox_events WHERE kafka_key=$1", workflowID).Scan(&count))
			require.Equal(t, 1, count)
		})
	}
}
