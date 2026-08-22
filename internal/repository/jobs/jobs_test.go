//nolint:testpackage // Tests unexported cursor helpers directly.
package jobs

import (
	"database/sql"
	"regexp"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
)

func TestJobLogsCursorRoundTrip(t *testing.T) {
	want := jobLogsCursor{
		SequenceNum: 42,
		Stream:      "stdout",
		EventID:     "log:job:stdout:42",
	}

	encoded := encodeJobLogsCursor(want)
	if encoded == "" {
		t.Fatal("expected encoded cursor")
	}

	got, err := extractDataFromGetJobLogsCursor(encoded)
	if err != nil {
		t.Fatalf("expected cursor to decode: %v", err)
	}
	if got != want {
		t.Fatalf("unexpected cursor: got %+v want %+v", got, want)
	}
}

func TestClaimJobQueryWaitsForRuntimeRowLock(t *testing.T) {
	query := claimJobQuery()

	assertContains(t, query, "ORDER BY rn.running_jobs ASC, rn.last_heartbeat_at DESC, rn.id ASC")
	assertContains(t, query, "FOR UPDATE")
	assertNotContains(t, query, "SKIP LOCKED")
	assertContains(t, query, "terminal_reason_code = NULL")
}

func TestClaimReplayValidationIsReadOnlyAndExact(t *testing.T) {
	t.Parallel()

	query := validateClaimReplayQuery()
	assertContains(t, query, "SELECT job.lease_expires_at")
	assertContains(t, query, "FROM jobs AS job")
	assertContains(t, query, "job.lease_token = $2")
	assertContains(t, query, "job.leased_by = $3")
	assertContains(t, query, "job.lease_process_instance_id = $4")
	assertContains(t, query, "job.dispatch_attempts = $5")
	assertContains(t, query, "job.lease_expires_at > clock_timestamp() AT TIME ZONE 'utc'")
	assertNotContains(t, query, "UPDATE")
	assertNotContains(t, query, "SET lease_expires_at")
}

func TestRecoveryReplayRenewsAndReturnsOnlyLiveExactAuthorities(t *testing.T) {
	t.Parallel()

	query := renewRecoveryReplayQuery()
	assertContains(t, query, "jsonb_to_recordset")
	assertContains(t, query, "job.lease_token = requested.lease_token")
	assertContains(t, query, "job.leased_by = $2")
	assertContains(t, query, "job.lease_process_instance_id = $3")
	assertContains(t, query, "job.status = 'RUNNING'")
	assertContains(t, query, "job.lease_expires_at > renewal.renewed_at")
	assertContains(t, query, "RETURNING job.id::text")
}

func TestLeaseRenewalCannotReviveExpiredAuthority(t *testing.T) {
	t.Parallel()

	query := renewJobLeaseQuery()
	assertContains(t, query, "job.lease_token = $2")
	assertContains(t, query, "job.status = 'RUNNING'")
	assertContains(t, query, "job.lease_expires_at > renewal.renewed_at")
	assertContains(t, query, "RETURNING job.lease_expires_at")
}

func TestNormalizeAttachJobContainerIdentityPreservesOpaqueRuntimeNodeID(t *testing.T) {
	t.Parallel()

	jobID, runtimeNodeID, err := normalizeAttachJobContainerIdentity(
		"550E8400-E29B-41D4-A716-446655440000",
		"local-docker",
	)
	if err != nil {
		t.Fatalf("normalizeAttachJobContainerIdentity() error = %v", err)
	}
	if jobID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("job ID = %q, want canonical UUID", jobID)
	}
	if runtimeNodeID != "local-docker" {
		t.Fatalf("runtime node ID = %q, want exact opaque identity", runtimeNodeID)
	}
}

func TestNormalizeAttachJobContainerIdentityRejectsEmptyRuntimeNodeID(t *testing.T) {
	t.Parallel()

	_, _, err := normalizeAttachJobContainerIdentity("550e8400-e29b-41d4-a716-446655440000", "")
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("normalizeAttachJobContainerIdentity() code = %s, want %s: %v", status.Code(err), codes.InvalidArgument, err)
	}
}

func TestAutomaticScheduleHashExcludesServerGeneratedTime(t *testing.T) {
	t.Parallel()

	fields := scheduleJobHashFields("workflow-1", "user-1", jobsmodel.JobTriggerAutomatic.ToString(), 3)
	if _, ok := fields["scheduled_at"]; ok {
		t.Fatal("automatic schedule hash includes server-generated scheduled_at")
	}
	if got := fields["workflow_generation"]; got != int64(3) {
		t.Fatalf("automatic schedule hash generation = %v, want 3", got)
	}
}

func TestManualScheduleHashMatchesMigrationContract(t *testing.T) {
	t.Parallel()

	fields := scheduleJobHashFields(
		"22222222-2222-4222-8222-222222222222",
		"11111111-1111-4111-8111-111111111111",
		jobsmodel.JobTriggerManual.ToString(),
		0,
	)
	hash, err := idempotency.HashCanonical(fields)
	if err != nil {
		t.Fatalf("HashCanonical() error = %v", err)
	}
	const migrationHash = "17fdb0dc876aca70a2bca5498d2df43622b843bb9f34448913ef5e31ce913a3f"
	if hash != migrationHash {
		t.Fatalf("manual schedule hash = %q, want migration contract %q", hash, migrationHash)
	}
}

func TestManualScheduleInsertDoesNotPermanentlyDeduplicateJobRow(t *testing.T) {
	t.Parallel()

	query, _, err := scheduleJobInsertStatement(
		"workflow-1",
		"user-1",
		time.Now(),
		jobsmodel.JobTriggerManual.ToString(),
		"command-key",
		0,
		false,
	)
	if err != nil {
		t.Fatalf("scheduleJobInsertStatement() error = %v", err)
	}
	assertNotContains(t, query, "ON CONFLICT")
	assertContains(t, query, "workflow_generation")
}

func TestAutomaticScheduleInsertPersistsGenerationForConflictValidation(t *testing.T) {
	t.Parallel()

	query, args, err := scheduleJobInsertStatement(
		"workflow-1",
		"user-1",
		time.Now(),
		jobsmodel.JobTriggerAutomatic.ToString(),
		"workflow:workflow-1:BUILD:3:automatic-job",
		3,
		true,
	)
	if err != nil {
		t.Fatalf("scheduleJobInsertStatement() error = %v", err)
	}
	if len(args) != 6 || args[5] != int64(3) {
		t.Fatalf("automatic schedule args = %#v, want workflow generation 3", args)
	}
	assertContains(t, query, "workflow_generation")
	assertContains(t, query, "RETURNING id, workflow_id, user_id, trigger, workflow_generation")
}

func TestValidateStoredAutomaticScheduleCommand(t *testing.T) {
	t.Parallel()

	requestHash, err := idempotency.HashCanonical(scheduleJobHashFields(
		"workflow-1", "user-1", jobsmodel.JobTriggerAutomatic.ToString(), 3,
	))
	if err != nil {
		t.Fatalf("HashCanonical() error = %v", err)
	}
	if err = validateStoredScheduleCommand(
		requestHash,
		"workflow-1",
		"user-1",
		jobsmodel.JobTriggerAutomatic.ToString(),
		sql.NullInt64{Int64: 3, Valid: true},
	); err != nil {
		t.Fatalf("validateStoredScheduleCommand() error = %v", err)
	}
	if code := status.Code(validateStoredScheduleCommand(
		requestHash,
		"workflow-1",
		"user-1",
		jobsmodel.JobTriggerAutomatic.ToString(),
		sql.NullInt64{},
	)); code != codes.AlreadyExists {
		t.Fatalf("unknown legacy generation code = %s, want %s", code, codes.AlreadyExists)
	}
	if code := status.Code(validateStoredScheduleCommand(
		requestHash,
		"workflow-1",
		"user-1",
		jobsmodel.JobTriggerAutomatic.ToString(),
		sql.NullInt64{Int64: 4, Valid: true},
	)); code != codes.AlreadyExists {
		t.Fatalf("changed generation code = %s, want %s", code, codes.AlreadyExists)
	}
}

func TestClaimJobQueryGatesOnlyContainerJobsOnRuntime(t *testing.T) {
	query := claimJobQuery()

	assertContains(t, query, "AND EXISTS (SELECT 1 FROM workflow WHERE kind = 'CONTAINER')")
	assertContains(t, query, "(SELECT kind FROM workflow) <> 'CONTAINER'")
	assertContains(t, query, "OR EXISTS (SELECT 1 FROM selected_runtime)")
	assertContains(t, query, "rn.running_jobs < rn.max_concurrency")
}

func TestClaimJobAndDeferralUseTheSameWorkflowBlockerPredicate(t *testing.T) {
	t.Parallel()

	blockedExpression := workflowClaimBlockedExpression("j")
	assertContains(t, claimJobQuery(), "AND NOT "+blockedExpression)
	assertContains(t, deferBlockedJobQuery(), "AND "+blockedExpression)
	assertContains(t, blockedExpression, "active.status = 'RUNNING'")
	assertContains(t, blockedExpression, "blocker.created_at < j.created_at")
	assertContains(t, blockedExpression, "blocker.created_at = j.created_at")
}

func TestGetReadyRuntimeNodeQueryIgnoresExecutionCapacity(t *testing.T) {
	query := getReadyRuntimeNodeQuery()

	assertContains(t, query, "WHERE status = 'READY'")
	assertContains(t, query, "last_heartbeat_at >")
	assertContains(t, query, "ORDER BY running_jobs ASC, last_heartbeat_at DESC, id ASC")
	assertNotContains(t, query, "running_jobs < max_concurrency")
}

func TestQueuedContainerJobMissingRuntimeQueryOnlyDiagnosesClaimableContainerJobs(t *testing.T) {
	query := queuedContainerJobMissingRuntimeQuery()

	assertContains(t, query, "j.id = $1")
	assertContains(t, query, "j.workflow_id = $2")
	assertContains(t, query, "j.status = 'QUEUED'")
	assertContains(t, query, "j.dispatch_attempts = $3")
	assertContains(t, query, "w.kind = 'CONTAINER'")
	assertContains(t, query, "rn.status = 'READY'")
	assertContains(t, query, "rn.last_heartbeat_at >")
	assertContains(t, query, "rn.running_jobs < rn.max_concurrency")
}

func TestReleaseJobForRetryQueryCarriesPreviousRuntimeOwner(t *testing.T) {
	query := releaseJobForRetryQuery()

	assertContains(t, query, "SELECT id, runtime_node_id")
	assertContains(t, query, "FOR UPDATE")
	assertContains(t, query, "RETURNING target.runtime_node_id AS previous_runtime_node_id")
	assertContains(t, query, "released.previous_runtime_node_id IS NOT NULL")
	assertContains(t, query, "rn.id = released.previous_runtime_node_id")
	assertContains(t, query, "terminal_reason_code = NULL")
}

func TestRecoverExpiredJobLeasesQueryPrefersStoredRuntimeEndpoint(t *testing.T) {
	query := recoverExpiredJobLeasesQuery()

	assertContains(t, query, "COALESCE(NULLIF(j.runtime_endpoint, ''), rn.docker_endpoint) AS runtime_endpoint")
}

func TestRecoverExpiredJobLeasesQueryIncludesNonReadyRuntimes(t *testing.T) {
	query := recoverExpiredJobLeasesQuery()

	assertContains(t, query, "rn.status IN ('UNHEALTHY', 'DRAINING')")
}

func TestRecoverExpiredJobLeasesQueryFlagsOnlyUnavailableRuntimeStates(t *testing.T) {
	expr := recoverExpiredRuntimeUnavailableExpression(t)

	assertContains(t, expr, "rn.id IS NULL")
	assertContains(t, expr, "rn.status = 'UNHEALTHY'")
	assertContains(t, expr, "rn.last_heartbeat_at <=")
	assertNotContains(t, expr, "rn.status = 'DRAINING'")
}

func assertContains(t *testing.T, value, want string) {
	t.Helper()

	if !strings.Contains(value, want) {
		t.Fatalf("expected query to contain %q:\n%s", want, value)
	}
}

func assertNotContains(t *testing.T, value, forbidden string) {
	t.Helper()

	if strings.Contains(value, forbidden) {
		t.Fatalf("expected query not to contain %q:\n%s", forbidden, value)
	}
}

func recoverExpiredRuntimeUnavailableExpression(t *testing.T) string {
	t.Helper()

	query := recoverExpiredJobLeasesQuery()
	start := strings.Index(query, "j.runtime_node_id IS NOT NULL")
	if start == -1 {
		t.Fatalf("expected query to contain runtime unavailable expression:\n%s", query)
	}
	end := strings.Index(query[start:], ") AS runtime_unavailable")
	if end == -1 {
		t.Fatalf("expected query to contain runtime unavailable alias:\n%s", query)
	}
	return query[start : start+end]
}

func TestEncodeJobLogsCursorEmpty(t *testing.T) {
	if got := encodeJobLogsCursor(jobLogsCursor{}); got != "" {
		t.Fatalf("expected empty cursor, got %q", got)
	}
}

func TestNewJobLogsHighlightToken(t *testing.T) {
	got, err := newJobLogsHighlightToken()
	if err != nil {
		t.Fatalf("expected token generation to succeed: %v", err)
	}

	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(got) {
		t.Fatalf("unexpected highlight token format: %q", got)
	}
}

func TestNewJobLogsSearchRequestUsesTokenScopedHighlightTags(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"

	got := newJobLogsSearchRequest("job_id = \"job_id\"", token, 201, jobsmodel.SearchJobLogsOptions{})

	if got.HighlightPreTag != "__CV_HL_START_"+token+"__" {
		t.Fatalf("unexpected highlight pre tag: %q", got.HighlightPreTag)
	}
	if got.HighlightPostTag != "__CV_HL_END_"+token+"__" {
		t.Fatalf("unexpected highlight post tag: %q", got.HighlightPostTag)
	}
	if got.Limit != 201 {
		t.Fatalf("unexpected limit: got %d want %d", got.Limit, 201)
	}
	if len(got.AttributesToHighlight) != 1 || got.AttributesToHighlight[0] != "message" {
		t.Fatalf("unexpected attributes to highlight: %+v", got.AttributesToHighlight)
	}
}

func TestNewJobLogsSearchRequestSupportsAscendingSort(t *testing.T) {
	got := newJobLogsSearchRequest("job_id = \"job_id\"", "", 201, jobsmodel.SearchJobLogsOptions{
		SortOrder: jobsmodel.JobLogsSortOrderAsc,
	})

	if len(got.Sort) != 2 || got.Sort[0] != "sequence_num:asc" || got.Sort[1] != "id:asc" {
		t.Fatalf("unexpected sort: %+v", got.Sort)
	}
}

func TestNewJobLogsSearchRequestCanDisableHighlights(t *testing.T) {
	got := newJobLogsSearchRequest("job_id = \"job_id\"", "", 201, jobsmodel.SearchJobLogsOptions{
		DisableHighlight: true,
	})

	if len(got.AttributesToHighlight) != 0 {
		t.Fatalf("unexpected attributes to highlight: %+v", got.AttributesToHighlight)
	}
	if got.HighlightPreTag != "" || got.HighlightPostTag != "" {
		t.Fatalf("unexpected highlight tags: pre=%q post=%q", got.HighlightPreTag, got.HighlightPostTag)
	}
}
