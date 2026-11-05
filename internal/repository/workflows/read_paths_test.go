//nolint:testpackage // Tests unexported read-error and replay helpers without widening production API.
package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
)

// TestMapWorkflowReadErrorReportsEveryOutcome proves a read distinguishes a
// missing workflow, a malformed identity and a real database failure.
func TestMapWorkflowReadErrorReportsEveryOutcome(t *testing.T) {
	t.Parallel()

	repo := New(&Config{}, nil)
	fault := errors.New("connection lost")
	for _, tc := range []struct {
		name, message string
		err           error
		wantCode      codes.Code
	}{
		{name: "Missing", err: pgx.ErrNoRows, wantCode: codes.NotFound, message: "workflow not found or not owned by user"},
		{name: "MalformedIdentity", err: &pgconn.PgError{Code: "22P02"}, wantCode: codes.InvalidArgument, message: "invalid workflow ID"},
		{name: "DatabaseFailure", err: fault, wantCode: codes.Internal, message: "failed to get workflow"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := repo.mapWorkflowReadError(tc.err, "workflow not found or not owned by user")
			if status.Code(err) != tc.wantCode || !strings.Contains(status.Convert(err).Message(), tc.message) {
				t.Fatalf("mapWorkflowReadError(%v) = %v, want %s containing %q", tc.err, err, tc.wantCode, tc.message)
			}
		})
	}
}

type replayCommitTx struct {
	pgx.Tx
	commitErr error
}

func (tx *replayCommitTx) Commit(context.Context) error { return tx.commitErr }

// TestReplayCreatedWorkflowReportsCommitAndDecodeFailures proves a replay does
// not report success when the transaction cannot commit or the stored snapshot
// cannot be decoded.
func TestReplayCreatedWorkflowReportsCommitAndDecodeFailures(t *testing.T) {
	t.Parallel()

	fault := errors.New("connection lost")
	for _, tc := range []struct {
		name        string
		tx          *replayCommitTx
		reservation *commandidempotency.Reservation
		message     string
	}{
		{
			name:        "CommitRefused",
			tx:          &replayCommitTx{commitErr: fault},
			reservation: &commandidempotency.Reservation{ResourceID: testWorkflowID},
			message:     "failed to commit idempotency replay",
		},
		{
			name:        "CorruptSnapshot",
			tx:          &replayCommitTx{},
			reservation: &commandidempotency.Reservation{ResourceID: testWorkflowID, Response: json.RawMessage("{")},
			message:     "failed to decode workflow replay",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := (&Repository{}).replayCreatedWorkflow(context.Background(), tc.tx, testUserID, tc.reservation)
			if res != nil || status.Code(err) != codes.Internal || !strings.Contains(status.Convert(err).Message(), tc.message) {
				t.Fatalf("replay = (%+v, %v), want nil and Internal containing %q", res, err, tc.message)
			}
		})
	}
}
