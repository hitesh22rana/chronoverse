//nolint:testpackage // Tests terminal replay without widening the production API.
package workflows

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIncrementFailureRejectsInvalidIdentities(t *testing.T) {
	t.Parallel()
	const jobID = "f972f8b6-3b12-4bcc-a730-ed546be37916"
	cases := []struct{ name, workflow, user, job, message string }{
		{"Workflow", "invalid", testUserID, jobID, "workflow ID"},
		{"User", testWorkflowID, "invalid", jobID, "user ID"},
		{"Job", testWorkflowID, testUserID, "invalid", "job ID"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reached, err := New(&Config{}, nil).IncrementWorkflowConsecutiveJobFailuresCount(context.Background(), tc.workflow, tc.user, tc.job)
			if reached || status.Code(err) != codes.InvalidArgument || !strings.Contains(status.Convert(err).Message(), tc.message) {
				t.Fatalf("increment = (%v, %v), want InvalidArgument naming %s", reached, err, tc.message)
			}
		})
	}
}

// terminalReplayRow supplies one stored terminal effect or a scan failure.
type terminalReplayRow struct {
	workflow, user, effect string
	threshold              bool
	err                    error
}

// Scan fills the terminal effect projection.
//
//nolint:errcheck // pgx supplies typed scan destinations; mismatches must fail the test.
func (r terminalReplayRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*string) = r.workflow
	*dest[1].(*string) = r.user
	*dest[2].(*string) = r.effect
	*dest[3].(*bool) = r.threshold
	return nil
}

// terminalReplayTx observes the replay lookup and commit without a database.
type terminalReplayTx struct {
	pgx.Tx
	t         *testing.T
	row       terminalReplayRow
	job       string
	commitErr error
	commits   int
}

// QueryRow verifies replay reads exactly the requested job.
func (tx *terminalReplayTx) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	tx.t.Helper()
	if !strings.Contains(query, "FROM workflow_terminal_effects") || !strings.Contains(query, "WHERE job_id = $1") || !reflect.DeepEqual(args, []any{tx.job}) {
		tx.t.Fatalf("replay lookup = %q, %v; want the terminal effect for %s", query, args, tx.job)
	}
	return tx.row
}

// Commit records attempts and returns the configured fault.
func (tx *terminalReplayTx) Commit(context.Context) error {
	tx.commits++
	return tx.commitErr
}

func TestReplayFailurePreservesResultAndRejectsConflictingIdentity(t *testing.T) {
	t.Parallel()
	const jobID = "f972f8b6-3b12-4bcc-a730-ed546be37916"
	fault := errors.New("connection lost")
	cases := []struct {
		name          string
		row           terminalReplayRow
		commitErr     error
		wantCode      codes.Code
		wantThreshold bool
		wantCommits   int
		message       string
	}{
		{name: "BelowThreshold", row: terminalReplayRow{workflow: testWorkflowID, user: testUserID, effect: terminalEffectFailed}, wantCommits: 1},
		{name: "ThresholdReached", row: terminalReplayRow{workflow: testWorkflowID, user: testUserID, effect: terminalEffectFailed, threshold: true}, wantThreshold: true, wantCommits: 1},
		{name: "ReadFailure", row: terminalReplayRow{err: fault}, wantCode: codes.Internal, message: "failed to fetch workflow terminal effect"},
		{name: "MissingEffect", row: terminalReplayRow{err: pgx.ErrNoRows}, wantCode: codes.Internal, message: "failed to fetch workflow terminal effect"},
		{name: "DifferentWorkflow", row: terminalReplayRow{workflow: testUserID, user: testUserID, effect: terminalEffectFailed}, wantCode: codes.AlreadyExists, message: "different effect"},
		{name: "DifferentUser", row: terminalReplayRow{workflow: testWorkflowID, user: testWorkflowID, effect: terminalEffectFailed}, wantCode: codes.AlreadyExists, message: "different effect"},
		{name: "CompletedEffect", row: terminalReplayRow{workflow: testWorkflowID, user: testUserID, effect: terminalEffectCompleted}, wantCode: codes.AlreadyExists, message: "different effect"},
		{
			name: "CommitFailure", row: terminalReplayRow{workflow: testWorkflowID, user: testUserID, effect: terminalEffectFailed, threshold: true},
			commitErr: fault, wantCode: codes.Internal, wantCommits: 1, message: "failed to commit terminal-effect replay",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tx := &terminalReplayTx{t: t, row: tc.row, job: jobID, commitErr: tc.commitErr}
			reached, err := replayWorkflowFailureEffect(context.Background(), tx, testWorkflowID, testUserID, jobID)
			if reached != tc.wantThreshold || status.Code(err) != tc.wantCode || tx.commits != tc.wantCommits || !strings.Contains(status.Convert(err).Message(), tc.message) {
				t.Fatalf("replay = (%v, %v), commits=%d; want threshold=%v code=%v commits=%d message containing %q", reached, err, tx.commits, tc.wantThreshold, tc.wantCode, tc.wantCommits, tc.message)
			}
		})
	}
}
