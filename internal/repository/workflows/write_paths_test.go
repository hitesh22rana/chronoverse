//nolint:testpackage // Tests the unexported write-error and build-identity helpers without widening production API.
package workflows

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
)

const (
	testImageRef    = "alpine:3.22.2"
	testImageDigest = "sha256:3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7e6f5a4b3c2d1e0f9a8b7c6d5e4f3a2b"
	testWorkflowID  = "550e8400-e29b-41d4-a716-446655440000"
	testUserID      = "9b2d4f1e-8a3c-4d5b-9e7f-0a1b2c3d4e5f"
	// testCompletedBuildStatus is the build status whose image identity the
	// guards below are stated in terms of.
	testCompletedBuildStatus = "COMPLETED"
)

// TestMapWorkflowWriteErrorKeepsCallerOutcome proves the wrapper the mutation
// paths share reports a caller that went away or ran out of time as exactly
// that, even when the failure arrives wrapped in a transport or driver error, and
// reports every other database failure as an internal one that keeps the call
// site context the caller needs to tell two failures apart.
func TestMapWorkflowWriteErrorKeepsCallerOutcome(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		err       error
		message   string
		wantCode  codes.Code
		wantCause string
	}{
		{
			name:      "DeadlineExceeded",
			err:       context.DeadlineExceeded,
			message:   "failed to commit transaction",
			wantCode:  codes.DeadlineExceeded,
			wantCause: context.DeadlineExceeded.Error(),
		},
		{
			// The wrapped text is preserved rather than reduced to the sentinel,
			// so the driver detail that led here is not lost on the way out.
			name:      "WrappedDeadlineExceeded",
			err:       fmt.Errorf("conn closed: %w", context.DeadlineExceeded),
			message:   "failed to update workflow",
			wantCode:  codes.DeadlineExceeded,
			wantCause: "conn closed: context deadline exceeded",
		},
		{
			name:      "Canceled",
			err:       context.Canceled,
			message:   "failed to start transaction",
			wantCode:  codes.Canceled,
			wantCause: context.Canceled.Error(),
		},
		{
			name:      "WrappedCanceled",
			err:       fmt.Errorf("write failed: %w", context.Canceled),
			message:   "failed to commit transaction",
			wantCode:  codes.Canceled,
			wantCause: "write failed: context canceled",
		},
		{
			name:      "DriverFailure",
			err:       errors.New("conn closed unexpectedly"),
			message:   "failed to commit transaction",
			wantCode:  codes.Internal,
			wantCause: "failed to commit transaction: conn closed unexpectedly",
		},
		{
			// A database error this wrapper does not classify stays internal: the
			// mapping to InvalidArgument belongs to the callers that inspect the
			// driver error themselves, and silently reclassifying it here would
			// report a client mistake as a server one in the wrong direction.
			name:      "UnclassifiedSQLState",
			err:       &pgconn.PgError{Code: "22P02", Message: "invalid input syntax for type uuid"},
			message:   "failed to update workflow",
			wantCode:  codes.Internal,
			wantCause: "failed to update workflow: : invalid input syntax for type uuid (SQLSTATE 22P02)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := mapWorkflowWriteError(tc.err, tc.message)
			if code := status.Code(got); code != tc.wantCode {
				t.Fatalf("mapWorkflowWriteError(%v) code = %v, want %v (err: %v)", tc.err, code, tc.wantCode, got)
			}
			if message := status.Convert(got).Message(); message != tc.wantCause {
				t.Fatalf("mapWorkflowWriteError(%v) message = %q, want %q", tc.err, message, tc.wantCause)
			}
		})
	}
}

// TestUpdateWorkflowBuildStatusRefusesGenerationsItCannotApply proves a build
// result for a generation that does not exist is refused before the command
// opens a transaction or reads a row, and is refused as the caller's own bad
// request rather than as a missing workflow. The repository is built without a
// database on purpose: there is nothing here it could reach, so a guard that
// stopped short of the transaction could not pass.
func TestUpdateWorkflowBuildStatusRefusesGenerationsItCannotApply(t *testing.T) {
	t.Parallel()

	repo := New(&Config{}, nil)

	for _, generation := range []int64{0, -1, -1000} {
		err := repo.UpdateWorkflowBuildStatus(
			context.Background(), testWorkflowID, testUserID, testCompletedBuildStatus, generation, testImageRef, testImageDigest,
		)
		if code := status.Code(err); code != codes.InvalidArgument {
			t.Fatalf("UpdateWorkflowBuildStatus(generation %d) code = %v, want %v (err: %v)", generation, code, codes.InvalidArgument, err)
		}
		want := "workflow generation must be at least 1"
		if message := status.Convert(err).Message(); message != want {
			t.Fatalf("UpdateWorkflowBuildStatus(generation %d) message = %q, want %q", generation, message, want)
		}
	}
}

// TestValidateBuildImageIdentityRejectsContradictoryCombinations proves the
// resolved image identity is accepted only where it means something: never on a
// build that has not completed, always for a completed container build, and
// never for a heartbeat build, which has no image at all.
func TestValidateBuildImageIdentityRejectsContradictoryCombinations(t *testing.T) {
	t.Parallel()

	var (
		container = workflowsmodel.KindContainer.ToString()
		heartbeat = workflowsmodel.KindHeartbeat.ToString()
		completed = workflowsmodel.WorkflowBuildStatusCompleted.ToString()
		queued    = workflowsmodel.WorkflowBuildStatusQueued.ToString()
		started   = workflowsmodel.WorkflowBuildStatusStarted.ToString()
		failed    = workflowsmodel.WorkflowBuildStatusFailed.ToString()
	)

	cases := []struct {
		name             string
		kind             string
		buildStatus      string
		resolvedImageRef string
		resolvedDigest   string
		wantCode         codes.Code
		wantMessage      string
	}{
		{
			name:             "QueuedBuildWithImage",
			kind:             container,
			buildStatus:      queued,
			resolvedImageRef: testImageRef,
			wantCode:         codes.InvalidArgument,
			wantMessage:      "resolved image identity is only valid for completed builds",
		},
		{
			name:             "StartedBuildWithImage",
			kind:             container,
			buildStatus:      started,
			resolvedImageRef: testImageRef,
			wantCode:         codes.InvalidArgument,
			wantMessage:      "resolved image identity is only valid for completed builds",
		},
		{
			name:           "FailedBuildWithDigest",
			kind:           container,
			buildStatus:    failed,
			resolvedDigest: testImageDigest,
			wantCode:       codes.InvalidArgument,
			wantMessage:    "resolved image identity is only valid for completed builds",
		},
		{
			name:        "QueuedBuildWithoutImage",
			kind:        container,
			buildStatus: queued,
		},
		{
			name:        "CompletedContainerWithoutImage",
			kind:        container,
			buildStatus: completed,
			wantCode:    codes.InvalidArgument,
			wantMessage: "completed container builds require image reference and digest",
		},
		{
			name:             "CompletedContainerWithoutDigest",
			kind:             container,
			buildStatus:      completed,
			resolvedImageRef: testImageRef,
			wantCode:         codes.InvalidArgument,
			wantMessage:      "completed container builds require image reference and digest",
		},
		{
			name:             "CompletedHeartbeatWithImage",
			kind:             heartbeat,
			buildStatus:      completed,
			resolvedImageRef: testImageRef,
			resolvedDigest:   testImageDigest,
			wantCode:         codes.InvalidArgument,
			wantMessage:      "heartbeat builds must not contain resolved image identity",
		},
		{
			name:             "CompletedHeartbeatWithDigestOnly",
			kind:             heartbeat,
			buildStatus:      completed,
			resolvedImageRef: "",
			resolvedDigest:   testImageDigest,
			wantCode:         codes.InvalidArgument,
			wantMessage:      "heartbeat builds must not contain resolved image identity",
		},
		{
			name:        "CompletedHeartbeatWithoutImage",
			kind:        heartbeat,
			buildStatus: completed,
		},
		{
			name:             "CompletedContainerWithImage",
			kind:             container,
			buildStatus:      completed,
			resolvedImageRef: testImageRef,
			resolvedDigest:   testImageDigest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := validateBuildImageIdentity(tc.kind, tc.buildStatus, tc.resolvedImageRef, tc.resolvedDigest)
			if code := status.Code(got); code != tc.wantCode {
				t.Fatalf(
					"validateBuildImageIdentity(%q, %q, %q, %q) code = %v, want %v (err: %v)",
					tc.kind, tc.buildStatus, tc.resolvedImageRef, tc.resolvedDigest, code, tc.wantCode, got,
				)
			}
			if tc.wantMessage != "" && status.Convert(got).Message() != tc.wantMessage {
				t.Fatalf(
					"validateBuildImageIdentity(%q, %q, %q, %q) message = %q, want %q",
					tc.kind, tc.buildStatus, tc.resolvedImageRef, tc.resolvedDigest, status.Convert(got).Message(), tc.wantMessage,
				)
			}
		})
	}
}

// TestCursorRoundTripPreservesPageBoundary proves a listing cursor carries exactly
// the identifier and creation instant the page boundary is defined by, and that an
// empty cursor stays empty rather than becoming a cursor no page can resume from.
// The listing path reads the cursor back on every subsequent request, so a
// cursor that does not survive its own encoding is a cursor the caller cannot use.
func TestCursorRoundTripPreservesPageBoundary(t *testing.T) {
	t.Parallel()

	// A creation instant with nanosecond precision and a non-UTC offset: both are
	// values the list query can produce and both must survive the round trip
	// unchanged, since the boundary comparison uses them exactly.
	createdAt := time.Date(2026, time.March, 14, 15, 9, 26, 535897932, time.FixedZone("UTC+5:30", 5*60*60+30*60))
	const workflowID = "550e8400-e29b-41d4-a716-446655440000"

	if encoded := encodeCursor(""); encoded != "" {
		t.Fatalf("encodeCursor(\"\") = %q, want the empty cursor unchanged", encoded)
	}

	raw := workflowID + string(delimiter) + createdAt.Format(time.RFC3339Nano)
	encoded := encodeCursor(raw)
	if encoded == "" || encoded == raw {
		t.Fatalf("encodeCursor(%q) = %q, want a non-empty encoded cursor", raw, encoded)
	}
	if decoded, err := base64.StdEncoding.DecodeString(encoded); err != nil || string(decoded) != raw {
		t.Fatalf("encodeCursor(%q) = %q, want it to be the base64 of the cursor itself (err %v)", raw, encoded, err)
	}

	// The caller receives the base64 form; the service decodes it before the
	// repository parses the boundary, so the round trip under test is the hop the
	// repository actually owns.
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode the cursor %q the service hands the repository: %v", encoded, err)
	}
	gotID, gotCreatedAt, err := extractDataFromCursor(string(decoded))
	if err != nil {
		t.Fatalf("extractDataFromCursor(%q) error = %v", encoded, err)
	}
	if gotID != workflowID {
		t.Fatalf("extractDataFromCursor(%q) id = %q, want %q", encoded, gotID, workflowID)
	}
	if !gotCreatedAt.Equal(createdAt) {
		t.Fatalf("extractDataFromCursor(%q) created_at = %s, want %s", encoded, gotCreatedAt, createdAt)
	}
}

// TestExtractDataFromCursorRefusesUnusableCursors proves a cursor the caller
// cannot have obtained from this API is refused as an invalid argument instead of
// being split into a boundary that silently lists the wrong page. The delimiter is
// the only framing, so a cursor with the wrong number of parts, or with a part
// that is not an instant, is a malformed request rather than a page boundary.
func TestExtractDataFromCursorRefusesUnusableCursors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		cursor      string
		wantMessage string
	}{
		{
			name:        "NoDelimiter",
			cursor:      "550e8400-e29b-41d4-a716-446655440000",
			wantMessage: "invalid cursor: expected two parts",
		},
		{
			name:        "EmptyCursor",
			cursor:      "",
			wantMessage: "invalid cursor: expected two parts",
		},
		{
			name:        "TooManyParts",
			cursor:      "550e8400-e29b-41d4-a716-446655440000$2026-03-14T15:09:26Z$extra",
			wantMessage: "invalid cursor: expected two parts",
		},
		{
			name:        "DelimiterOnly",
			cursor:      string(delimiter),
			wantMessage: "invalid timestamp",
		},
		{
			name:        "InstantIsNotATimestamp",
			cursor:      "550e8400-e29b-41d4-a716-446655440000$not-a-timestamp",
			wantMessage: "invalid timestamp",
		},
		{
			name:        "DateWithoutTime",
			cursor:      "550e8400-e29b-41d4-a716-446655440000$2026-03-14",
			wantMessage: "invalid timestamp",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			id, createdAt, err := extractDataFromCursor(tc.cursor)
			if code := status.Code(err); code != codes.InvalidArgument {
				t.Fatalf("extractDataFromCursor(%q) code = %v, want %v (err: %v)", tc.cursor, code, codes.InvalidArgument, err)
			}
			if message := status.Convert(err).Message(); !strings.HasPrefix(message, tc.wantMessage) {
				t.Fatalf("extractDataFromCursor(%q) message = %q, want it to start with %q", tc.cursor, message, tc.wantMessage)
			}
			if id != "" || !createdAt.IsZero() {
				t.Fatalf("extractDataFromCursor(%q) = (%q, %s), want an empty boundary alongside the refusal", tc.cursor, id, createdAt)
			}
		})
	}
}
