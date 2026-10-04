//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package jobs

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

const (
	// recoveryWorkerID is the recovery worker identity used across these tests.
	recoveryWorkerID = "integration-recovery-worker"
	// recoveryLeaseDuration is the renewed lease handed to a recovering worker.
	recoveryLeaseDuration = 30 * time.Second
	// expiredFixtureMinutes is deliberately far in the past so fixture leases are
	// unambiguously older than any expired job another test leaves behind. The
	// recovery scan is global, so a fixture that merely happened to be expired
	// could be pushed out of a limited batch by unrelated rows.
	expiredFixtureMinutes = 60
	// activeFixtureMinutes keeps an "unexpired" fixture lease valid no matter how
	// slow container-backed setup and the race detector are.
	activeFixtureMinutes = 120
)

// leaseOwnership is the durable lease-ownership surface of one running job.
// LeaseProcessInstanceID names the jobs.lease_process_instance_id column; no
// returned model projects it, so it is only observable on the durable row.
type leaseOwnership struct {
	ID                     string
	Status                 string
	LeaseToken             sql.NullString
	LeasedBy               sql.NullString
	LeaseProcessInstanceID sql.NullString
	LeaseExpiresAt         sql.NullTime
	LastHeartbeatAt        sql.NullTime
	ContainerID            sql.NullString
	RuntimeNodeID          sql.NullString
	RuntimeEndpoint        sql.NullString
	Attempts               int32
	DispatchAttempts       int32
	CompletedAt            sql.NullTime
	TerminalReasonCode     sql.NullString
}

func readLeaseOwnership(ctx context.Context, t *testing.T, pg *postgres.Postgres, jobID string) leaseOwnership {
	t.Helper()

	var ownership leaseOwnership
	if err := pg.QueryRow(ctx, `
		SELECT id::text, status, lease_token, leased_by, lease_process_instance_id, lease_expires_at,
			last_heartbeat_at, container_id, runtime_node_id, runtime_endpoint, attempts,
			dispatch_attempts, completed_at, terminal_reason_code
		FROM jobs
		WHERE id = $1
	`, jobID).Scan(
		&ownership.ID, &ownership.Status, &ownership.LeaseToken, &ownership.LeasedBy,
		&ownership.LeaseProcessInstanceID, &ownership.LeaseExpiresAt, &ownership.LastHeartbeatAt,
		&ownership.ContainerID, &ownership.RuntimeNodeID, &ownership.RuntimeEndpoint,
		&ownership.Attempts, &ownership.DispatchAttempts, &ownership.CompletedAt,
		&ownership.TerminalReasonCode,
	); err != nil {
		t.Fatalf("read lease ownership for %q: %v", jobID, err)
	}
	return ownership
}

// setLeaseMinutes rewrites a job's lease expiry and heartbeat from a
// database-relative timestamp. The offset is bound as data, never interpolated
// into SQL, so expiry ordering never depends on client clock skew or on sleeping.
func setLeaseMinutes(ctx context.Context, t *testing.T, pg *postgres.Postgres, jobID string, minutes int) {
	t.Helper()

	tag, err := pg.Exec(ctx, `
		UPDATE jobs
		SET lease_expires_at = (now() AT TIME ZONE 'utc') + make_interval(mins => $2),
			last_heartbeat_at = (now() AT TIME ZONE 'utc') + make_interval(mins => $2)
		WHERE id = $1
	`, jobID, minutes)
	if err != nil {
		t.Fatalf("set lease expiry for %q: %v", jobID, err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("set lease expiry for %q affected %d rows, want 1", jobID, tag.RowsAffected())
	}
}

// setExpiredFixtureLeases gives every listed job the same expiry in one
// statement, which is what the query's `id ASC` tiebreak needs to be decided by
// job identity instead of by the order the fixtures happened to be seeded in.
func setExpiredFixtureLeases(ctx context.Context, t *testing.T, pg *postgres.Postgres, minutes int, jobIDs ...string) {
	t.Helper()

	tag, err := pg.Exec(ctx, `
		UPDATE jobs
		SET lease_expires_at = (now() AT TIME ZONE 'utc') + make_interval(mins => $2),
			last_heartbeat_at = (now() AT TIME ZONE 'utc') + make_interval(mins => $2)
		WHERE id = ANY($1::uuid[])
	`, jobIDs, minutes)
	if err != nil {
		t.Fatalf("set identical lease expiries: %v", err)
	}
	if tag.RowsAffected() != int64(len(jobIDs)) {
		t.Fatalf("set identical lease expiries affected %d rows, want %d", tag.RowsAffected(), len(jobIDs))
	}
}

// setRuntimeNodeState forces the runtime node that actually owns the job's slot
// into a specific status and heartbeat age, and restores the exact prior values
// afterwards. Claim may attribute the job to any healthy node, so this test
// mutates a row it may not own and must give it back untouched.
func setRuntimeNodeState(ctx context.Context, t *testing.T, pg *postgres.Postgres, nodeID, status string, heartbeatAgeSeconds int) {
	t.Helper()

	var priorStatus string
	var priorHeartbeat time.Time
	var priorRunningJobs int
	if err := pg.QueryRow(ctx, `SELECT status, last_heartbeat_at, running_jobs FROM runtime_nodes WHERE id = $1`, nodeID).
		Scan(&priorStatus, &priorHeartbeat, &priorRunningJobs); err != nil {
		t.Fatalf("snapshot runtime node %q: %v", nodeID, err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		if _, err := pg.Exec(cleanupCtx, `
			UPDATE runtime_nodes
			SET status = $2::runtime_node_status, last_heartbeat_at = $3, running_jobs = $4
			WHERE id = $1
		`, nodeID, priorStatus, priorHeartbeat, priorRunningJobs); err != nil {
			t.Errorf("restore runtime node %q: %v", nodeID, err)
		}
	})

	if _, err := pg.Exec(ctx, `
		UPDATE runtime_nodes
		SET status = $2::runtime_node_status,
			last_heartbeat_at = (now() AT TIME ZONE 'utc') - make_interval(secs => $3)
		WHERE id = $1
	`, nodeID, status, heartbeatAgeSeconds); err != nil {
		t.Fatalf("set runtime node %q state: %v", nodeID, err)
	}
}

func recoveredJobIDs(jobs []*jobsmodel.ExpiredJobLease) []string {
	ids := make([]string, 0, len(jobs))
	for _, job := range jobs {
		if job != nil {
			ids = append(ids, job.ID)
		}
	}
	return ids
}

func recoveredJobByID(jobs []*jobsmodel.ExpiredJobLease, jobID string) *jobsmodel.ExpiredJobLease {
	for _, job := range jobs {
		if job != nil && job.ID == jobID {
			return job
		}
	}
	return nil
}

// assertRecovered asserts every expected job id was handed back. The recovery
// scan is global, so the response legitimately may also contain leases that
// belong to other tests; only membership of the fixture's own ids is asserted.
func assertRecovered(t *testing.T, jobs []*jobsmodel.ExpiredJobLease, want []string) {
	t.Helper()

	got := recoveredJobIDs(jobs)
	for _, jobID := range want {
		if !slices.Contains(got, jobID) {
			t.Fatalf("recovery response %v does not contain fixture job %q", got, jobID)
		}
	}
}

// assertNotRecovered asserts none of the listed jobs were handed back.
func assertNotRecovered(t *testing.T, jobs []*jobsmodel.ExpiredJobLease, unwanted []string) {
	t.Helper()

	got := recoveredJobIDs(jobs)
	for _, jobID := range unwanted {
		if slices.Contains(got, jobID) {
			t.Fatalf("recovery response %v unexpectedly contains job %q", got, jobID)
		}
	}
}

// assertReturnedLease checks the lease authority carried back to the recovering
// worker. The process instance is only observable on the durable job row, which
// assertRecoveredOwnership covers.
func assertReturnedLease(t *testing.T, job *jobsmodel.ExpiredJobLease, workerID string) {
	t.Helper()

	if job == nil {
		t.Fatal("expected the recovered job in the response")
	}
	if !strings.HasPrefix(job.LeaseToken, workerID+":") {
		t.Fatalf("returned lease token = %q, want the %q prefix", job.LeaseToken, workerID+":")
	}
	assertNullString(t, "returned leased_by", job.LeasedBy, workerID)
}

func assertRecoveredOwnership(ctx context.Context, t *testing.T, pg *postgres.Postgres, jobID, workerID, processInstanceID string) {
	t.Helper()

	state := readLeaseOwnership(ctx, t, pg, jobID)
	if state.Status != "RUNNING" {
		t.Fatalf("recovered job %q status = %q, want %q", jobID, state.Status, "RUNNING")
	}
	if !state.LeaseToken.Valid || !strings.HasPrefix(state.LeaseToken.String, workerID+":") {
		t.Fatalf("job %q lease_token = %q, want the %q prefix", jobID, state.LeaseToken.String, workerID+":")
	}
	assertNullString(t, "leased_by", state.LeasedBy, workerID)
	assertNullString(t, "lease_process_instance_id", state.LeaseProcessInstanceID, processInstanceID)
	if !state.LeaseExpiresAt.Valid {
		t.Fatalf("job %q lease_expires_at is NULL, want a renewed lease", jobID)
	}
	if !state.LastHeartbeatAt.Valid {
		t.Fatalf("job %q last_heartbeat_at is NULL, want a recovery heartbeat", jobID)
	}
}

func assertUnrecoveredOwnership(ctx context.Context, t *testing.T, pg *postgres.Postgres, jobID, leaseToken string) {
	t.Helper()

	state := readLeaseOwnership(ctx, t, pg, jobID)
	if state.LeaseToken.String != leaseToken {
		t.Fatalf("job %q lease_token = %q, want the abandoned %q", jobID, state.LeaseToken.String, leaseToken)
	}
}

func readWorkerCommand(ctx context.Context, t *testing.T, pg *postgres.Postgres, processInstanceID, commandID string) (*jobCommandRow, bool) {
	t.Helper()

	var row jobCommandRow
	err := pg.QueryRow(ctx, `
		SELECT status, request_hash, resource_id, response, completed_at, expires_at
		FROM command_idempotency_keys
		WHERE scope = $1 AND operation = $2 AND idempotency_key = $3
	`, commandidempotency.WorkerScope(processInstanceID), commandidempotency.OperationJobRecoverExpiredLeases, commandID,
	).Scan(&row.Status, &row.RequestHash, &row.ResourceID, &row.Response, &row.CompletedAt, &row.ExpiresAt)
	if pg.IsNoRows(err) {
		return nil, false
	}
	if err != nil {
		t.Fatalf("read recovery command ledger row: %v", err)
	}
	return &row, true
}

func TestIntegrationRecoverExpiredJobLeasesTakesOverOnlyExpiredLeases(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	expired := seedClaimedJob(ctx, t, pg, repo)
	setLeaseMinutes(ctx, t, pg, expired.JobID, -expiredFixtureMinutes)

	active := seedClaimedJob(ctx, t, pg, repo)
	setLeaseMinutes(ctx, t, pg, active.JobID, activeFixtureMinutes)
	activeBefore := readLeaseOwnership(ctx, t, pg, active.JobID)

	processInstanceID := uuid.NewString()
	commandID := "recover-" + fixtureTag()
	jobs, err := repo.RecoverExpiredJobLeases(ctx, 10, recoveryWorkerID, processInstanceID, commandID, recoveryLeaseDuration)
	if err != nil {
		t.Fatalf("RecoverExpiredJobLeases: %v", err)
	}

	assertRecovered(t, jobs, []string{expired.JobID})
	assertNotRecovered(t, jobs, []string{active.JobID})
	assertReturnedLease(t, recoveredJobByID(jobs, expired.JobID), recoveryWorkerID)

	// Ownership transfers wholesale: new token, worker, process, expiry and
	// heartbeat, while the job keeps running and keeps its retry counters.
	recovered := readLeaseOwnership(ctx, t, pg, expired.JobID)
	if recovered.LeaseToken.String == expired.LeaseToken {
		t.Fatalf("recovered lease_token still equals the abandoned %q", expired.LeaseToken)
	}
	if recovered.Attempts != 1 || recovered.DispatchAttempts != 1 {
		t.Fatalf("recovered attempts/dispatch_attempts = %d/%d, want 1/1 (recovery must not consume a retry)", recovered.Attempts, recovered.DispatchAttempts)
	}
	if recovered.CompletedAt.Valid {
		t.Fatalf("recovered job completed_at = %v, want NULL", recovered.CompletedAt.Time.UTC())
	}
	assertRecoveredOwnership(ctx, t, pg, expired.JobID, recoveryWorkerID, processInstanceID)

	// The live lease of an unexpired job is untouched.
	activeAfter := readLeaseOwnership(ctx, t, pg, active.JobID)
	if activeAfter.LeaseToken.String != activeBefore.LeaseToken.String ||
		activeAfter.LeasedBy.String != activeBefore.LeasedBy.String ||
		activeAfter.LeaseProcessInstanceID.String != activeBefore.LeaseProcessInstanceID.String ||
		!activeAfter.LeaseExpiresAt.Time.Equal(activeBefore.LeaseExpiresAt.Time) {
		t.Fatalf("active job ownership changed: before %+v after %+v", activeBefore, activeAfter)
	}

	// The abandoned worker token is dead: only the recovering identity can now
	// terminate the job.
	if err := failJobTerminal(ctx, repo, expired.JobID, expired.LeaseToken, "fail-old-owner-"+fixtureTag()); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("FailJob with the abandoned lease code = %v, want %v (err: %v)", status.Code(err), codes.FailedPrecondition, err)
	}
	if got := readLeaseOwnership(ctx, t, pg, expired.JobID).Status; got != "RUNNING" {
		t.Fatalf("job status = %q, want %q after the abandoned owner was rejected", got, "RUNNING")
	}

	command, ok := readWorkerCommand(ctx, t, pg, processInstanceID, commandID)
	if !ok {
		t.Fatalf("command ledger has no row for the completed recovery command %q", commandID)
	}
	if command.Status != "COMPLETED" {
		t.Fatalf("recovery command status = %q, want %q", command.Status, "COMPLETED")
	}
}

func TestIntegrationRecoverExpiredJobLeasesAppliesDefaultBatchSizeAndWorkerIdentity(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	const expiredJobs = 3
	fixtures := make([]claimedFixture, 0, expiredJobs)
	for range expiredJobs {
		fixture := seedClaimedJob(ctx, t, pg, repo)
		setLeaseMinutes(ctx, t, pg, fixture.JobID, -expiredFixtureMinutes)
		fixtures = append(fixtures, fixture)
	}

	processInstanceID := uuid.NewString()
	// A non-positive batch size and an empty worker ID must both fall back to
	// their documented defaults rather than failing or reclaiming nothing.
	jobs, err := repo.RecoverExpiredJobLeases(ctx, 0, "", processInstanceID, "recover-defaults-"+fixtureTag(), recoveryLeaseDuration)
	if err != nil {
		t.Fatalf("RecoverExpiredJobLeases(defaults): %v", err)
	}

	jobIDs := make([]string, 0, len(fixtures))
	for _, fixture := range fixtures {
		jobIDs = append(jobIDs, fixture.JobID)
	}
	// The default batch of 100 must cover every fixture, so nothing seeded here
	// may be left behind.
	assertRecovered(t, jobs, jobIDs)
	for _, fixture := range fixtures {
		assertReturnedLease(t, recoveredJobByID(jobs, fixture.JobID), "execution-worker-recovery")
		assertRecoveredOwnership(ctx, t, pg, fixture.JobID, "execution-worker-recovery", processInstanceID)
	}
}

func TestIntegrationRecoverExpiredJobLeasesOrdersByExpiryAndHonorsBatchLimit(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	// Distinct, database-relative expiries make the documented
	// "lease_expires_at ASC, id ASC" selection observable without sleeping, and
	// keep every fixture older than any expired job another test may have left.
	offsets := []int{60, 50, 40, 30}
	fixtures := make([]claimedFixture, 0, len(offsets))
	for _, offset := range offsets {
		fixture := seedClaimedJob(ctx, t, pg, repo)
		setLeaseMinutes(ctx, t, pg, fixture.JobID, -offset)
		fixtures = append(fixtures, fixture)
	}

	processInstanceID := uuid.NewString()
	first, err := repo.RecoverExpiredJobLeases(ctx, 2, recoveryWorkerID, processInstanceID, "recover-limit-1-"+fixtureTag(), recoveryLeaseDuration)
	if err != nil {
		t.Fatalf("RecoverExpiredJobLeases(first batch): %v", err)
	}

	// The driving statement is an UPDATE with no ORDER BY, so the response order
	// is unspecified. Compare the selection as a set: the LIMIT must have picked
	// exactly the two oldest expiries, which is the ordering contract.
	assertSelected(t, first, []string{fixtures[0].JobID, fixtures[1].JobID})
	for _, fixture := range fixtures[:2] {
		assertReturnedLease(t, recoveredJobByID(first, fixture.JobID), recoveryWorkerID)
		assertRecoveredOwnership(ctx, t, pg, fixture.JobID, recoveryWorkerID, processInstanceID)
	}
	// The jobs outside the batch keep their abandoned leases for the next wave.
	assertNotRecovered(t, first, []string{fixtures[2].JobID, fixtures[3].JobID})
	for _, fixture := range fixtures[2:] {
		assertUnrecoveredOwnership(ctx, t, pg, fixture.JobID, fixture.LeaseToken)
	}

	second, err := repo.RecoverExpiredJobLeases(ctx, 2, recoveryWorkerID, processInstanceID, "recover-limit-2-"+fixtureTag(), recoveryLeaseDuration)
	if err != nil {
		t.Fatalf("RecoverExpiredJobLeases(second batch): %v", err)
	}
	assertSelected(t, second, []string{fixtures[2].JobID, fixtures[3].JobID})
	for _, fixture := range fixtures[2:] {
		assertRecoveredOwnership(ctx, t, pg, fixture.JobID, recoveryWorkerID, processInstanceID)
	}
}

// TestIntegrationRecoverExpiredJobLeasesBreaksExpiryTiesByJobID covers the
// documented `id ASC` tiebreak: with byte-identical expiries the LIMIT must pick
// the smaller job id, not the order the fixtures were seeded in.
func TestIntegrationRecoverExpiredJobLeasesBreaksExpiryTiesByJobID(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	lower := seedClaimedJob(ctx, t, pg, repo)
	higher := seedClaimedJob(ctx, t, pg, repo)
	lowerID, higherID := lower.JobID, higher.JobID
	if lowerID > higherID {
		lowerID, higherID = higherID, lowerID
	}
	setExpiredFixtureLeases(ctx, t, pg, -expiredFixtureMinutes, lower.JobID, higher.JobID)
	if readLeaseOwnership(ctx, t, pg, lower.JobID).LeaseExpiresAt.Time !=
		readLeaseOwnership(ctx, t, pg, higher.JobID).LeaseExpiresAt.Time {
		t.Fatal("tie fixtures did not receive byte-identical lease expiries")
	}

	jobs, err := repo.RecoverExpiredJobLeases(ctx, 1, recoveryWorkerID, uuid.NewString(), "recover-tie-"+fixtureTag(), recoveryLeaseDuration)
	if err != nil {
		t.Fatalf("RecoverExpiredJobLeases(tie break): %v", err)
	}

	assertSelected(t, jobs, []string{lowerID})
	assertNotRecovered(t, jobs, []string{higherID})
	assertUnrecoveredOwnership(ctx, t, pg, higher.JobID, higher.LeaseToken)
}

// assertSelectedFixtureIDs asserts the response contains exactly the expected
// job ids. It is only sound when the fixtures are provably the oldest eligible
// leases in the table, which these fixtures guarantee by aging themselves far
// past any job another test could leave behind.
func assertSelected(t *testing.T, jobs []*jobsmodel.ExpiredJobLease, want []string) {
	t.Helper()

	got := recoveredJobIDs(jobs)
	slices.Sort(got)
	wantSorted := slices.Clone(want)
	slices.Sort(wantSorted)
	if !slices.Equal(got, wantSorted) {
		t.Fatalf("selected %v, want exactly %v", got, wantSorted)
	}
}

func TestIntegrationRecoverExpiredJobLeasesReplayRenewsStoredIdentity(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixtures := []claimedFixture{seedClaimedJob(ctx, t, pg, repo), seedClaimedJob(ctx, t, pg, repo)}
	for i, fixture := range fixtures {
		setLeaseMinutes(ctx, t, pg, fixture.JobID, -expiredFixtureMinutes+i)
	}
	jobIDs := []string{fixtures[0].JobID, fixtures[1].JobID}

	processInstanceID := uuid.NewString()
	commandID := "recover-replay-" + fixtureTag()
	first, err := repo.RecoverExpiredJobLeases(ctx, 10, recoveryWorkerID, processInstanceID, commandID, recoveryLeaseDuration)
	if err != nil {
		t.Fatalf("RecoverExpiredJobLeases: %v", err)
	}
	assertRecovered(t, first, jobIDs)
	firstExpiry := make(map[string]time.Time, len(first))
	for _, job := range first {
		firstExpiry[job.ID] = readLeaseOwnership(ctx, t, pg, job.ID).LeaseExpiresAt.Time
	}

	replayed, err := repo.RecoverExpiredJobLeases(ctx, 10, recoveryWorkerID, processInstanceID, commandID, recoveryLeaseDuration)
	if err != nil {
		t.Fatalf("RecoverExpiredJobLeases (idempotent replay): %v", err)
	}

	// A replay must hand back the same authorities, not re-run the takeover.
	assertRecovered(t, replayed, jobIDs)
	for _, fixture := range fixtures {
		original := recoveredJobByID(first, fixture.JobID)
		replayJob := recoveredJobByID(replayed, fixture.JobID)
		if replayJob == nil {
			t.Fatalf("replay dropped recovered job %q", fixture.JobID)
		}
		if replayJob.LeaseToken != original.LeaseToken {
			t.Fatalf("replayed lease token for %q = %q, want the stored %q", fixture.JobID, replayJob.LeaseToken, original.LeaseToken)
		}
		assertNullString(t, "replayed leased_by", replayJob.LeasedBy, recoveryWorkerID)

		state := readLeaseOwnership(ctx, t, pg, fixture.JobID)
		assertNullString(t, "lease_process_instance_id", state.LeaseProcessInstanceID, processInstanceID)
		if !state.LeaseExpiresAt.Time.After(firstExpiry[fixture.JobID]) {
			t.Fatalf("replayed lease_expires_at for %q = %v, want renewed past %v", fixture.JobID, state.LeaseExpiresAt.Time.UTC(), firstExpiry[fixture.JobID].UTC())
		}
		if state.Attempts != 1 || state.DispatchAttempts != 1 {
			t.Fatalf("replayed attempts/dispatch_attempts for %q = %d/%d, want 1/1", fixture.JobID, state.Attempts, state.DispatchAttempts)
		}
	}

	command, ok := readWorkerCommand(ctx, t, pg, processInstanceID, commandID)
	if !ok {
		t.Fatalf("command ledger has no row for the recovery command %q", commandID)
	}
	if command.Status != "COMPLETED" {
		t.Fatalf("recovery command status = %q, want %q", command.Status, "COMPLETED")
	}
}

func TestIntegrationRecoverExpiredJobLeasesReplayCannotResurrectDeadEntries(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	reexpired := seedClaimedJob(ctx, t, pg, repo)
	superseded := seedClaimedJob(ctx, t, pg, repo)
	terminal := seedClaimedJob(ctx, t, pg, repo)
	for i, fixture := range []claimedFixture{reexpired, superseded, terminal} {
		setLeaseMinutes(ctx, t, pg, fixture.JobID, -expiredFixtureMinutes+i)
	}

	processInstanceID := uuid.NewString()
	commandID := "recover-dead-replay-" + fixtureTag()
	first, err := repo.RecoverExpiredJobLeases(ctx, 10, recoveryWorkerID, processInstanceID, commandID, recoveryLeaseDuration)
	if err != nil {
		t.Fatalf("RecoverExpiredJobLeases: %v", err)
	}
	jobIDs := []string{reexpired.JobID, superseded.JobID, terminal.JobID}
	assertRecovered(t, first, jobIDs)

	// Each stored entry dies in a different, individually durable way.
	setLeaseMinutes(ctx, t, pg, reexpired.JobID, -1)
	supersedingToken := "other-worker:" + superseded.LeaseToken
	if _, supersedeErr := pg.Exec(ctx, `UPDATE jobs SET lease_token = $2 WHERE id = $1`, superseded.JobID, supersedingToken); supersedeErr != nil {
		t.Fatalf("supersede lease token: %v", supersedeErr)
	}
	if _, terminalizeErr := pg.Exec(ctx, `
		UPDATE jobs
		SET status = 'FAILED',
			completed_at = now() AT TIME ZONE 'utc',
			lease_token = NULL, leased_by = NULL, lease_process_instance_id = NULL,
			lease_expires_at = NULL, last_heartbeat_at = NULL,
			failure_kind = 'SYSTEM', terminal_reason_code = 'SYSTEM_ERROR'
		WHERE id = $1
	`, terminal.JobID); terminalizeErr != nil {
		t.Fatalf("terminalize job: %v", terminalizeErr)
	}

	reexpiredBefore := readLeaseOwnership(ctx, t, pg, reexpired.JobID)
	commandBefore, ok := readWorkerCommand(ctx, t, pg, processInstanceID, commandID)
	if !ok {
		t.Fatalf("command ledger has no row for the recovery command %q", commandID)
	}

	replayed, err := repo.RecoverExpiredJobLeases(ctx, 10, recoveryWorkerID, processInstanceID, commandID, recoveryLeaseDuration)
	if err != nil {
		t.Fatalf("RecoverExpiredJobLeases (replay of dead entries): %v", err)
	}
	// The stored response is global, so the replay may still hand back leases that
	// are legitimately alive. What must never come back is a dead fixture entry.
	assertNotRecovered(t, replayed, jobIDs)

	// Replay must not have renewed the re-expired lease either.
	reexpiredAfter := readLeaseOwnership(ctx, t, pg, reexpired.JobID)
	if !reexpiredAfter.LeaseExpiresAt.Time.Equal(reexpiredBefore.LeaseExpiresAt.Time) {
		t.Fatalf("re-expired lease_expires_at = %v, want unchanged %v", reexpiredAfter.LeaseExpiresAt.Time.UTC(), reexpiredBefore.LeaseExpiresAt.Time.UTC())
	}
	assertUnrecoveredOwnership(ctx, t, pg, superseded.JobID, supersedingToken)
	terminalAfter := readLeaseOwnership(ctx, t, pg, terminal.JobID)
	if terminalAfter.Status != "FAILED" || terminalAfter.LeaseToken.Valid {
		t.Fatalf("terminal job = %+v, want an untouched FAILED row", terminalAfter)
	}
	assertNullString(t, "terminal job terminal_reason_code", terminalAfter.TerminalReasonCode, "SYSTEM_ERROR")

	commandAfter, ok := readWorkerCommand(ctx, t, pg, processInstanceID, commandID)
	if !ok {
		t.Fatalf("command ledger lost the row for %q", commandID)
	}
	assertSameLedgerRow(t, commandAfter, commandBefore)
}

func TestIntegrationRecoverExpiredJobLeasesRejectsConflictingCommandPayload(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedClaimedJob(ctx, t, pg, repo)
	setLeaseMinutes(ctx, t, pg, fixture.JobID, -expiredFixtureMinutes)

	processInstanceID := uuid.NewString()
	commandID := "recover-conflict-" + fixtureTag()
	accepted, err := repo.RecoverExpiredJobLeases(ctx, 10, recoveryWorkerID, processInstanceID, commandID, recoveryLeaseDuration)
	if err != nil {
		t.Fatalf("RecoverExpiredJobLeases: %v", err)
	}
	assertRecovered(t, accepted, []string{fixture.JobID})
	acceptedToken := readLeaseOwnership(ctx, t, pg, fixture.JobID).LeaseToken.String
	commandAfterFirst, ok := readWorkerCommand(ctx, t, pg, processInstanceID, commandID)
	if !ok {
		t.Fatalf("command ledger has no row for %q", commandID)
	}

	conflicts := []struct {
		name          string
		batchSize     int32
		workerID      string
		leaseDuration time.Duration
	}{
		{name: "different worker", batchSize: 10, workerID: "other-recovery-worker", leaseDuration: recoveryLeaseDuration},
		{name: "different batch size", batchSize: 1, workerID: recoveryWorkerID, leaseDuration: recoveryLeaseDuration},
		{name: "different lease duration", batchSize: 10, workerID: recoveryWorkerID, leaseDuration: time.Minute},
	}
	for _, conflict := range conflicts {
		t.Run(conflict.name, func(t *testing.T) {
			_, conflictErr := repo.RecoverExpiredJobLeases(
				ctx, conflict.batchSize, conflict.workerID, processInstanceID, commandID, conflict.leaseDuration,
			)
			if status.Code(conflictErr) != codes.AlreadyExists {
				t.Fatalf("RecoverExpiredJobLeases(conflicting payload) code = %v, want %v (err: %v)", status.Code(conflictErr), codes.AlreadyExists, conflictErr)
			}
			if got := readLeaseOwnership(ctx, t, pg, fixture.JobID).LeaseToken.String; got != acceptedToken {
				t.Fatalf("job %q lease_token = %q, want the originally granted %q", fixture.JobID, got, acceptedToken)
			}
			commandAfterConflict, ok := readWorkerCommand(ctx, t, pg, processInstanceID, commandID)
			if !ok {
				t.Fatalf("command ledger lost the row for %q", commandID)
			}
			assertSameLedgerRow(t, commandAfterConflict, commandAfterFirst)
		})
	}

	// The recovery command identity is scoped per process instance, so another
	// process reusing the same command ID starts a fresh command instead of
	// colliding. It must still not be able to steal the renewed lease.
	otherProcessID := uuid.NewString()
	otherProcess, err := repo.RecoverExpiredJobLeases(ctx, 10, recoveryWorkerID, otherProcessID, commandID, recoveryLeaseDuration)
	if err != nil {
		t.Fatalf("RecoverExpiredJobLeases(other process instance): %v", err)
	}
	assertNotRecovered(t, otherProcess, []string{fixture.JobID})
	if got := readLeaseOwnership(ctx, t, pg, fixture.JobID).LeaseToken.String; got != acceptedToken {
		t.Fatalf("job %q lease_token = %q, want the first process's %q", fixture.JobID, got, acceptedToken)
	}
	if _, ok := readWorkerCommand(ctx, t, pg, otherProcessID, commandID); !ok {
		t.Fatalf("the second process instance did not record its own command %q", commandID)
	}
}

func TestIntegrationRecoverExpiredJobLeasesRetainsRuntimeOwnershipAndSlots(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedClaimedJob(ctx, t, pg, repo)
	setLeaseMinutes(ctx, t, pg, fixture.JobID, -expiredFixtureMinutes)
	// Recovery inspects a Docker container, so the fixture needs a real identity.
	if _, err := pg.Exec(ctx, `UPDATE jobs SET container_id = $2, runtime_endpoint = '' WHERE id = $1`, fixture.JobID, "recovery-container-"+fixtureTag()); err != nil {
		t.Fatalf("seed container id: %v", err)
	}
	claimed := readLeaseOwnership(ctx, t, pg, fixture.JobID)
	nodeID, occupied := occupySpareSlots(ctx, t, pg, fixture.JobID)
	var expectedEndpoint string
	if endpointErr := pg.QueryRow(ctx, `SELECT docker_endpoint FROM runtime_nodes WHERE id = $1`, nodeID).Scan(&expectedEndpoint); endpointErr != nil {
		t.Fatalf("read runtime endpoint fallback: %v", endpointErr)
	}

	processInstanceID := uuid.NewString()
	jobs, err := repo.RecoverExpiredJobLeases(ctx, 10, recoveryWorkerID, processInstanceID, "recover-runtime-"+fixtureTag(), recoveryLeaseDuration)
	if err != nil {
		t.Fatalf("RecoverExpiredJobLeases: %v", err)
	}
	assertRecovered(t, jobs, []string{fixture.JobID})

	// Taking over an expired lease continues the same execution on the same
	// runtime, so the slot must stay occupied exactly once.
	if got := readRuntimeRunningJobs(ctx, t, pg, nodeID); got != occupied {
		t.Fatalf("runtime running_jobs = %d after recovery, want unchanged %d", got, occupied)
	}

	recovered := readLeaseOwnership(ctx, t, pg, fixture.JobID)
	assertNullString(t, "runtime_node_id", recovered.RuntimeNodeID, nodeID)
	assertNullString(t, "runtime_endpoint", recovered.RuntimeEndpoint, claimed.RuntimeEndpoint.String)
	assertNullString(t, "container_id", recovered.ContainerID, claimed.ContainerID.String)

	returned := recoveredJobByID(jobs, fixture.JobID)
	assertReturnedLease(t, returned, recoveryWorkerID)
	assertNullString(t, "returned runtime_node_id", returned.RuntimeNodeID, nodeID)
	assertNullString(t, "returned runtime_endpoint", returned.RuntimeEndpoint, expectedEndpoint)
	assertNullString(t, "returned container_id", returned.ContainerID, claimed.ContainerID.String)
	if returned.RuntimeUnavailable {
		t.Fatalf("returned runtime_unavailable = true for a fresh READY runtime, want false")
	}
	if !returned.LogRetention {
		t.Fatal("returned log_retention = false, want the durable workflow setting")
	}
}

func TestIntegrationRecoverExpiredJobLeasesHonorsRuntimeAvailabilityContract(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	// RuntimeHeartbeatTTL is 1 minute and RuntimeLostAfter is 5 minutes in the
	// test repository config, so a READY node with a 3 minute old heartbeat sits
	// inside both windows and is deliberately left to its current owner.
	tests := []struct {
		name                string
		status              string
		heartbeatAgeSeconds int
		detachRuntime       bool
		wantRecovered       bool
		wantRuntimeUnusable bool
	}{
		{name: "ready and fresh", status: "READY", heartbeatAgeSeconds: 5, wantRecovered: true},
		{name: "ready inside the lost window", status: "READY", heartbeatAgeSeconds: 180},
		{name: "ready but heartbeat lost", status: "READY", heartbeatAgeSeconds: 600, wantRecovered: true, wantRuntimeUnusable: true},
		{name: "draining still allows cleanup", status: "DRAINING", heartbeatAgeSeconds: 5, wantRecovered: true},
		{name: "unhealthy", status: "UNHEALTHY", heartbeatAgeSeconds: 5, wantRecovered: true, wantRuntimeUnusable: true},
		{name: "no runtime at all", status: "READY", heartbeatAgeSeconds: 5, detachRuntime: true, wantRecovered: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := seedClaimedJob(ctx, t, pg, repo)
			setLeaseMinutes(ctx, t, pg, fixture.JobID, -expiredFixtureMinutes)
			nodeID := mustClaimedRuntimeNode(ctx, t, pg, fixture.JobID)
			setRuntimeNodeState(ctx, t, pg, nodeID, testCase.status, testCase.heartbeatAgeSeconds)
			if testCase.detachRuntime {
				if _, err := pg.Exec(ctx, `UPDATE jobs SET runtime_node_id = NULL WHERE id = $1`, fixture.JobID); err != nil {
					t.Fatalf("detach runtime node: %v", err)
				}
			}
			abandonedLease := readLeaseOwnership(ctx, t, pg, fixture.JobID).LeaseToken.String

			jobs, err := repo.RecoverExpiredJobLeases(
				ctx, 10, recoveryWorkerID, uuid.NewString(), "recover-runtime-state-"+fixtureTag(), recoveryLeaseDuration,
			)
			if err != nil {
				t.Fatalf("RecoverExpiredJobLeases: %v", err)
			}

			recoveredJob := recoveredJobByID(jobs, fixture.JobID)
			if !testCase.wantRecovered {
				if recoveredJob != nil {
					t.Fatalf("job %q was recovered, want it left to its current owner", fixture.JobID)
				}
				assertUnrecoveredOwnership(ctx, t, pg, fixture.JobID, abandonedLease)
				return
			}
			if recoveredJob == nil {
				t.Fatalf("job %q was not recovered, want recovery for runtime state %+v", fixture.JobID, testCase)
			}
			if recoveredJob.RuntimeUnavailable != testCase.wantRuntimeUnusable {
				t.Fatalf("runtime_unavailable = %v, want %v", recoveredJob.RuntimeUnavailable, testCase.wantRuntimeUnusable)
			}
			// The stored runtime endpoint is always carried to the recovering
			// worker, including when the node reference is gone.
			if testCase.detachRuntime {
				if recoveredJob.RuntimeNodeID.Valid {
					t.Fatalf("returned runtime_node_id = %q, want NULL for a detached runtime", recoveredJob.RuntimeNodeID.String)
				}
			} else {
				assertNullString(t, "returned runtime_node_id", recoveredJob.RuntimeNodeID, nodeID)
			}
			assertNullString(t, "returned runtime_endpoint", recoveredJob.RuntimeEndpoint, readLeaseOwnership(ctx, t, pg, fixture.JobID).RuntimeEndpoint.String)
		})
	}
}

func TestIntegrationRecoverExpiredJobLeasesSkipsRunningJobsWithoutUsableLease(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	tokenless := seedClaimedJob(ctx, t, pg, repo)
	setLeaseMinutes(ctx, t, pg, tokenless.JobID, -expiredFixtureMinutes)
	if _, err := pg.Exec(ctx, `UPDATE jobs SET lease_token = NULL WHERE id = $1`, tokenless.JobID); err != nil {
		t.Fatalf("clear lease token: %v", err)
	}

	expiryless := seedClaimedJob(ctx, t, pg, repo)
	if _, err := pg.Exec(ctx, `UPDATE jobs SET lease_expires_at = NULL WHERE id = $1`, expiryless.JobID); err != nil {
		t.Fatalf("clear lease expiry: %v", err)
	}

	tokenlessBefore := readLeaseOwnership(ctx, t, pg, tokenless.JobID)
	expirylessBefore := readLeaseOwnership(ctx, t, pg, expiryless.JobID)
	skipped := []string{tokenless.JobID, expiryless.JobID}

	processInstanceID := uuid.NewString()
	commandID := "recover-no-lease-" + fixtureTag()
	jobs, err := repo.RecoverExpiredJobLeases(ctx, 10, recoveryWorkerID, processInstanceID, commandID, recoveryLeaseDuration)
	if err != nil {
		t.Fatalf("RecoverExpiredJobLeases: %v", err)
	}
	assertNotRecovered(t, jobs, skipped)

	// A job without a lease token keeps its current owner identity: recovery
	// found nothing to take over, not something to strip.
	tokenlessAfter := readLeaseOwnership(ctx, t, pg, tokenless.JobID)
	if tokenlessAfter.LeaseToken.Valid {
		t.Fatalf("tokenless job gained a lease token: %q", tokenlessAfter.LeaseToken.String)
	}
	if tokenlessAfter.LeasedBy.String != tokenlessBefore.LeasedBy.String {
		t.Fatalf("tokenless job leased_by = %q, want unchanged %q", tokenlessAfter.LeasedBy.String, tokenlessBefore.LeasedBy.String)
	}
	if !tokenlessAfter.LeaseExpiresAt.Time.Equal(tokenlessBefore.LeaseExpiresAt.Time) {
		t.Fatalf("tokenless job lease_expires_at = %v, want unchanged %v", tokenlessAfter.LeaseExpiresAt.Time.UTC(), tokenlessBefore.LeaseExpiresAt.Time.UTC())
	}
	expirylessAfter := readLeaseOwnership(ctx, t, pg, expiryless.JobID)
	if expirylessAfter.LeaseExpiresAt.Valid {
		t.Fatalf("expiry-less job gained a lease expiry: %v", expirylessAfter.LeaseExpiresAt.Time.UTC())
	}
	if expirylessAfter.LeasedBy.String != expirylessBefore.LeasedBy.String {
		t.Fatalf("expiry-less job leased_by = %q, want unchanged %q", expirylessAfter.LeasedBy.String, expirylessBefore.LeasedBy.String)
	}

	// An empty result is still a completed command with a durable replay record.
	command, ok := readWorkerCommand(ctx, t, pg, processInstanceID, commandID)
	if !ok {
		t.Fatalf("command ledger has no row for the empty recovery command %q", commandID)
	}
	if command.Status != "COMPLETED" {
		t.Fatalf("recovery command status = %q, want %q", command.Status, "COMPLETED")
	}
	replayed, err := repo.RecoverExpiredJobLeases(ctx, 10, recoveryWorkerID, processInstanceID, commandID, recoveryLeaseDuration)
	if err != nil {
		t.Fatalf("RecoverExpiredJobLeases (empty replay): %v", err)
	}
	assertNotRecovered(t, replayed, skipped)
}

func TestIntegrationRecoverExpiredJobLeasesRejectsMalformedProcessIdentity(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedClaimedJob(ctx, t, pg, repo)
	setLeaseMinutes(ctx, t, pg, fixture.JobID, -expiredFixtureMinutes)

	commandID := "recover-malformed-" + fixtureTag()
	_, err := repo.RecoverExpiredJobLeases(ctx, 10, recoveryWorkerID, "not-a-uuid", commandID, recoveryLeaseDuration)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("RecoverExpiredJobLeases(malformed process id) code = %v, want %v (err: %v)", status.Code(err), codes.InvalidArgument, err)
	}
	if count := countCommandRowsByKey(ctx, t, pg, commandidempotency.OperationJobRecoverExpiredLeases, commandID); count != 0 {
		t.Fatalf("command ledger holds %d recovery rows for a malformed process identity", count)
	}
	assertUnrecoveredOwnership(ctx, t, pg, fixture.JobID, fixture.LeaseToken)
}
