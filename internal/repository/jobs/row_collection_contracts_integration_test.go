//nolint:testpackage // Verifies decoding boundaries using real database driver rows.
package jobs

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

// These SQL responses deliberately exercise the collection helpers' input
// contracts. The production recovery queries do not return NULL identities or
// malformed schemas; no persisted job or command is modified by these tests.
func TestIntegrationRecoveryCollectionDriverFailures(t *testing.T) {
	pg := testkit.Postgres(t)
	projection := `SELECT 'id'::text AS id, 'workflow'::text AS workflow_id, 'user'::text AS user_id,
 NULL::text AS container_id, 'token'::text AS lease_token, NULL::text AS leased_by, 'MANUAL'::text AS trigger,
 now() AS scheduled_at, 1::int4 AS attempts, true AS log_retention, NULL::text AS runtime_node_id,
 NULL::text AS runtime_endpoint, false AS runtime_unavailable`
	for _, tc := range []struct {
		name, query, message string
		collect              func(pgx.Rows) error
	}{
		{
			name: "replay identity scan", query: `SELECT NULL::text`, message: "failed to collect renewed recovery replay leases",
			collect: func(rows pgx.Rows) error {
				result, err := collectRecoveryReplayIDs(rows)
				if result != nil {
					t.Errorf("failed collection returned identities: %v", result)
				}
				return err
			},
		},
		{
			name: "replay stream error", query: `SELECT CASE WHEN i=1 THEN 'first' ELSE (1/(i-i))::text END FROM generate_series(1,2) AS i`, message: "division by zero",
			collect: func(rows pgx.Rows) error {
				result, err := collectRecoveryReplayIDs(rows)
				if result != nil {
					t.Errorf("failed stream returned partial identities: %v", result)
				}
				return err
			},
		},
		{
			name: "lease timestamp scan", query: strings.Replace(projection, "now() AS scheduled_at", "NULL::timestamptz AS scheduled_at", 1), message: "failed to collect expired job leases",
			collect: func(rows pgx.Rows) error {
				result, err := collectExpiredJobLeases(rows)
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
			collect: func(rows pgx.Rows) error {
				result, err := collectExpiredJobLeases(rows)
				if result != nil {
					t.Errorf("failed stream returned partial leases: %v", result)
				}
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := pg.Query(t.Context(), tc.query)
			if err != nil {
				t.Fatalf("query must produce driver Rows before collection fails: %v", err)
			}
			err = tc.collect(rows)
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

func TestDecodeCancelJobSnapshotContracts(t *testing.T) {
	for _, payload := range []string{`[`, `[]`, `{"Attempt":"not-a-number"}`} {
		t.Run(payload, func(t *testing.T) {
			result, err := decodeCancelJobSnapshot([]byte(payload))
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
