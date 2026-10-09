//nolint:testpackage // Exercises transaction failure paths in claim helpers.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

const (
	claimTestJobID   = "claim-job"
	staleClaimReason = "stored lease is no longer active"
)

type claimRow struct {
	values []any
	err    error
}

func (r claimRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i, value := range r.values {
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(value))
	}
	return nil
}

type claimTx struct {
	pgx.Tx
	t          *testing.T
	rows       []claimRow
	execTags   []string
	execErrors []error
	commitErr  error
	commits    int
	queries    int
	execs      int
	queryArgs  []any
	response   map[string]any
}

func (tx *claimTx) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	tx.t.Helper()
	tx.queryArgs = args
	if tx.queries >= len(tx.rows) {
		tx.t.Fatal("unexpected query")
	}
	row := tx.rows[tx.queries]
	tx.queries++
	return row
}

func (tx *claimTx) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	tx.t.Helper()
	if tx.execs >= len(tx.execTags) {
		tx.t.Fatal("unexpected write")
	}
	i := tx.execs
	tx.execs++
	if len(args) == 7 {
		encoded, ok := args[5].([]byte)
		if !ok {
			tx.t.Fatal("completion response is not encoded JSON")
		}
		if err := json.Unmarshal(encoded, &tx.response); err != nil {
			tx.t.Fatal(err)
		}
	}
	var err error
	if i < len(tx.execErrors) {
		err = tx.execErrors[i]
	}
	return pgconn.NewCommandTag(tx.execTags[i]), err
}
func (tx *claimTx) Commit(context.Context) error { tx.commits++; return tx.commitErr }

//nolint:gocyclo // Keeps outcome and transaction assertions together.
func TestClaimReplayTransactionOutcomes(t *testing.T) {
	expiry := time.Now().UTC().Add(time.Minute)
	claimed := &jobsmodel.ClaimedJob{ID: claimTestJobID, LeaseToken: "token"}
	positive, err := json.Marshal(map[string]any{"claimed": true, "job": claimed})
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("database unavailable")
	cases := []struct {
		name             string
		response         []byte
		row              claimRow
		commitErr        error
		code             codes.Code
		ok               bool
		reason           string
		queries, commits int
	}{
		{name: "corrupt stored response", response: []byte("{"), code: codes.Internal},
		{name: "negative replay", response: []byte(`{"claimed":false,"reason":"already terminal"}`), reason: "already terminal", commits: 1},
		{name: "missing stored job", response: []byte(`{"claimed":true,"reason":"missing"}`), reason: "missing", commits: 1},
		{name: "negative commit canceled", response: []byte(`{"claimed":false}`), commitErr: context.Canceled, code: codes.Canceled, commits: 1},
		{name: "active authority", response: positive, row: claimRow{values: []any{expiry}}, ok: true, queries: 1, commits: 1},
		{name: "expired authority", response: positive, row: claimRow{err: pgx.ErrNoRows}, reason: staleClaimReason, queries: 1, commits: 1},
		{name: "validation read failed", response: positive, row: claimRow{err: failure}, code: codes.Internal, queries: 1},
		{name: "validation timed out", response: positive, row: claimRow{err: context.DeadlineExceeded}, code: codes.DeadlineExceeded, queries: 1},
		{name: "positive commit failed", response: positive, row: claimRow{values: []any{expiry}}, commitErr: failure, code: codes.Internal, queries: 1, commits: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := &claimTx{t: t, rows: []claimRow{tc.row}, commitErr: tc.commitErr}
			repo := &Repository{pg: &postgres.Postgres{}}
			got, ok, reason, err := repo.replayJobClaim(context.Background(), tx, tc.response, claimTestJobID, "worker", "process", 3)
			if status.Code(err) != tc.code || ok != tc.ok || reason != tc.reason {
				t.Fatalf("replay = %+v, %t, %q, %v", got, ok, reason, err)
			}
			if tx.queries != tc.queries || tx.commits != tc.commits || tx.execs != 0 {
				t.Fatalf("transaction queries/commits/writes = %d/%d/%d", tx.queries, tx.commits, tx.execs)
			}
			if tc.ok && (got.ID != claimTestJobID || got.LeaseToken != "token" || !got.LeaseExpiresAt.Equal(expiry)) {
				t.Fatalf("replayed authority = %+v", got)
			}
			if !tc.ok && got != nil {
				t.Fatalf("returned unauthorized job: %+v", got)
			}
			if tx.queries > 0 && !reflect.DeepEqual(tx.queryArgs, []any{claimTestJobID, "token", "worker", "process", int32(3)}) {
				t.Fatalf("authority arguments = %#v", tx.queryArgs)
			}
		})
	}
}

func TestClaimRejectionTransactionOutcomes(t *testing.T) {
	failure := errors.New("database unavailable")
	cases := []struct {
		name       string
		blocked    bool
		rows       []claimRow
		execErrors []error
		commitErr  error
		code       codes.Code
		reason     string
		commits    int
	}{
		{name: "defer write failed", execErrors: []error{failure}, code: codes.Internal},
		{name: "blocked", blocked: true, reason: "job deferred behind another workflow job", commits: 1},
		{name: "blocked completion failed", blocked: true, execErrors: []error{nil, failure}, code: codes.Internal},
		{name: "blocked commit failed", blocked: true, commitErr: failure, code: codes.Internal, commits: 1},
		{name: "runtime read failed", rows: []claimRow{{err: failure}}, code: codes.Internal},
		{name: "runtime unavailable", rows: []claimRow{{values: []any{true}}}, code: codes.Unavailable},
		{name: "missing job", rows: []claimRow{{values: []any{false}}, {err: pgx.ErrNoRows}}, code: codes.NotFound},
		{name: "status read failed", rows: []claimRow{{values: []any{false}}, {err: failure}}, code: codes.Internal},
		{name: "terminal", rows: []claimRow{{values: []any{false}}, {values: []any{"COMPLETED", int32(1)}}}, reason: "job status is COMPLETED", commits: 1},
		{name: "stale generation", rows: []claimRow{{values: []any{false}}, {values: []any{"QUEUED", int32(4)}}}, reason: "dispatch attempt mismatch: current 4", commits: 1},
		{name: "rejection completion failed", rows: []claimRow{{values: []any{false}}, {values: []any{"QUEUED", int32(4)}}}, execErrors: []error{nil, failure}, code: codes.Internal},
		{name: "rejection commit failed", rows: []claimRow{{values: []any{false}}, {values: []any{"QUEUED", int32(4)}}}, commitErr: failure, code: codes.Internal, commits: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tag := "UPDATE 0"
			if tc.blocked {
				tag = "UPDATE 1"
			}
			tx := &claimTx{t: t, rows: tc.rows, execTags: []string{tag, "UPDATE 1"}, execErrors: tc.execErrors, commitErr: tc.commitErr}
			repo := &Repository{pg: &postgres.Postgres{}, cfg: &Config{RuntimeHeartbeatTTL: time.Minute}}
			reason, err := repo.rejectJobClaim(context.Background(), tx, claimTestJobID, "workflow", 3, "scope", "command", "hash")
			if reason != tc.reason || status.Code(err) != tc.code || tx.commits != tc.commits {
				t.Fatalf("rejection = %q, %v; commits %d", reason, err, tx.commits)
			}
			if tc.code == codes.OK && (tx.response["claimed"] != false || tx.response["reason"] != tc.reason) {
				t.Fatalf("stored response = %#v", tx.response)
			}
			if tc.code == codes.Internal && !strings.Contains(err.Error(), "database unavailable") {
				t.Fatalf("lost database failure: %v", err)
			}
		})
	}
}
