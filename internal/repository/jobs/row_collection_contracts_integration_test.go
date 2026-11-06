//nolint:testpackage // Verifies decoding boundaries using real database driver rows.
package jobs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

// These SQL responses deliberately exercise the collection helpers' input
// contracts. The production recovery queries do not return NULL identities or
// malformed schemas; no persisted job or command is modified by these tests.
func TestIntegrationRecoveryCollectionDriverFailures(t *testing.T) {
	pg := testkit.Postgres(t)
	repo := New(&Config{}, nil, pg, nil, nil, nil, nil)
	scope := commandidempotency.WorkerScope(uuid.NewString())
	projection := `SELECT 'id'::text AS id, 'workflow'::text AS workflow_id, 'user'::text AS user_id,
 NULL::text AS container_id, 'token'::text AS lease_token, NULL::text AS leased_by, 'MANUAL'::text AS trigger,
 now() AS scheduled_at, 1::int4 AS attempts, true AS log_retention, NULL::text AS runtime_node_id,
 NULL::text AS runtime_endpoint, false AS runtime_unavailable`
	for _, tc := range []struct {
		name, query, message string
		collect              func(*testing.T, pgx.Tx, pgx.Rows) error
	}{
		{
			name: "replay identity scan", query: `SELECT NULL::text`, message: "failed to collect renewed recovery replay leases",
			collect: func(t *testing.T, _ pgx.Tx, rows pgx.Rows) error {
				t.Helper()
				result, err := collectRenewedRecoveryJobs(rows, []*jobsmodel.ExpiredJobLease{{ID: "first", LeaseToken: "token"}})
				if result != nil {
					t.Errorf("failed collection returned identities: %v", result)
				}
				return err
			},
		},
		{
			name: "replay stream error", query: `SELECT CASE WHEN i=1 THEN 'first' ELSE (1/(i-i))::text END FROM generate_series(1,2) AS i`, message: "division by zero",
			collect: func(t *testing.T, _ pgx.Tx, rows pgx.Rows) error {
				t.Helper()
				result, err := collectRenewedRecoveryJobs(rows, []*jobsmodel.ExpiredJobLease{{ID: "first", LeaseToken: "token"}})
				if result != nil {
					t.Errorf("failed stream returned partial identities: %v", result)
				}
				return err
			},
		},
		{
			name: "lease timestamp scan", query: strings.Replace(projection, "now() AS scheduled_at", "NULL::timestamptz AS scheduled_at", 1), message: "failed to collect expired job leases",
			collect: func(t *testing.T, tx pgx.Tx, rows pgx.Rows) error {
				t.Helper()
				result, err := repo.completeExpiredRecovery(t.Context(), tx, scope, "driver-contract", strings.Repeat("0", 64), rows)
				if result != nil {
					t.Errorf("failed collection returned leases: %v", result)
				}
				return err
			},
		},
		{
			name:    "lease stream error",
			query:   strings.Replace(projection, "'id'::text AS id", "CASE WHEN i=1 THEN 'first' ELSE (1/(i-i))::text END AS id", 1) + ` FROM generate_series(1,2) AS i`,
			message: "division by zero",
			collect: func(t *testing.T, tx pgx.Tx, rows pgx.Rows) error {
				t.Helper()
				result, err := repo.completeExpiredRecovery(t.Context(), tx, scope, "driver-contract", strings.Repeat("0", 64), rows)
				if result != nil {
					t.Errorf("failed stream returned partial leases: %v", result)
				}
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := pg.BeginTx(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
					t.Errorf("rollback driver contract transaction: %v", rollbackErr)
				}
			})
			rows, err := tx.Query(t.Context(), tc.query)
			if err != nil {
				t.Fatalf("query must produce driver Rows before collection fails: %v", err)
			}
			err = tc.collect(t, tx, rows)
			if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("collection error=%v, want Internal/%q", err, tc.message)
			}
			// pgx.CollectRows owns closing its result set, including on scan errors.
			if rows.Next() {
				t.Fatal("collection left rows open")
			}
		})
	}
}

func TestIntegrationFinishCancelReplayRejectsMalformedResponse(t *testing.T) {
	pg := testkit.Postgres(t)
	for _, payload := range []string{`[`, `[]`, `{"Attempt":"not-a-number"}`} {
		t.Run(payload, func(t *testing.T) {
			tx, err := pg.BeginTx(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
					t.Errorf("rollback rejected cancel replay: %v", rollbackErr)
				}
			})
			result, err := finishCancelReplay(t.Context(), tx, []byte(payload))
			if result != nil || status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "failed to decode cancel replay") {
				t.Fatalf("snapshot=%+v error=%v", result, err)
			}
		})
	}
}

func TestIntegrationLogCollectionRejectsIncompatibleDriverResponse(t *testing.T) {
	ch := testkit.ClickHouse(t)
	rows, err := ch.Query(t.Context(), `SELECT 'invalid-timestamp', 'message', toUInt32(1), 'stdout', 'event'`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close log driver rows: %v", closeErr)
		}
	}()
	logs, cursor, err := collectJobLogPage(rows, 2)
	if logs != nil || cursor != (jobLogsCursor{}) || status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "failed to scan logs") {
		t.Fatalf("logs=%v cursor=%+v error=%v", logs, cursor, err)
	}
}

func TestIntegrationLogCollectionRejectsStreamingServerFailure(t *testing.T) {
	ch := testkit.ClickHouse(t)
	// One-row blocks let the driver receive a valid row before the next block's
	// integer division fails. This is a real Rows.Err contract, not a schema mock.
	rows, err := ch.Query(t.Context(), `SELECT now64(9), toString(intDiv(1, toInt64(number)-1)), toUInt32(number), 'stdout', toString(number)
 FROM numbers(2) SETTINGS max_block_size=1, max_threads=1`)
	if err != nil {
		t.Fatalf("query must return Rows before stream failure: %v", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && !errors.Is(closeErr, rows.Err()) {
			t.Errorf("close failed log stream: %v", closeErr)
		}
	}()
	logs, cursor, err := collectJobLogPage(rows, 2)
	if logs != nil || cursor != (jobLogsCursor{}) || status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "rows error") {
		t.Fatalf("logs=%v cursor=%+v error=%v", logs, cursor, err)
	}
	if !strings.Contains(err.Error(), "division") && !strings.Contains(err.Error(), "Division") {
		t.Fatalf("missing original server cause: %v", err)
	}
}
