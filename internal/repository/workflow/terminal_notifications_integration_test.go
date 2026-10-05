//nolint:testpackage // Durable replay tests drive the unexported terminal handlers against real repositories.
package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	notificationsmodel "github.com/hitesh22rana/chronoverse/internal/model/notifications"
	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	kafkapkg "github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
	"github.com/hitesh22rana/chronoverse/internal/repository/notifications"
	"github.com/hitesh22rana/chronoverse/internal/repository/workflows"
)

const (
	// durableTerminalJobFailedTitle, durableTerminalJobCompletedTitle and
	// durableTerminalTerminatedTitle are the exact titles the terminal handlers
	// publish. A notification identity is derived from the title, so the tests
	// must spell them the way the handlers do.
	durableTerminalJobFailedTitle    = "Job Execution Failed"
	durableTerminalJobCompletedTitle = "Job Execution Completed"
	durableTerminalTerminatedTitle   = "Workflow Terminated"
	// durableTerminalCleanupTimeout bounds every fixture teardown statement so a
	// stuck container cannot hang the suite.
	durableTerminalCleanupTimeout = 15 * time.Second
)

// durableTerminalNotificationRow is one durable notifications row. The replay
// invariant is about the stored row, not about a request key the handler could
// re-derive, so tests compare ids, kinds, payloads and keys together.
type durableTerminalNotificationRow struct {
	ID             string
	Kind           string
	Payload        string
	IdempotencyKey string
}

// durableTerminalEffectRow is one workflow_terminal_effects ledger row. JobID is
// the durable identity the workflows repository keys the ledger on, so a
// redelivery cannot add a second row for the same job.
type durableTerminalEffectRow struct {
	JobID            string
	Effect           string
	ThresholdReached sql.NullBool
}

// durableTerminalFixture is one unique user plus one built workflow, wired to
// the real workflows and notifications repositories behind in-process service
// adapters, so the terminal handlers run against actual PostgreSQL state.
type durableTerminalFixture struct {
	pg            *postgres.Postgres
	repo          *Repository
	workflows     *workflows.Repository
	notifications durableTerminalNotificationsClient
	tag           string
	name          string
	userID        string
	workflowID    string
}

// TestMain provisions the shared PostgreSQL container the durable terminal
// replay tests need. The pure orchestration unit tests in this package never
// touch it because testkit starts services lazily on first use.
func TestMain(m *testing.M) {
	durableTerminalRunSuite(m)
}

// durableTerminalRunSuite keeps the testkit bootstrap in a prefixed helper so
// the only package-level name this file introduces is TestMain itself.
func durableTerminalRunSuite(m *testing.M) {
	testkit.Run(m, testkit.WithPostgres())
}

// TestIntegrationTerminalFailureReplayKeepsDurableEffects proves a redelivered
// JOB_FAILED event is a no-op on durable state. The threshold failure publishes
// two independent effects - the job failure and the workflow termination - under
// two different deterministic identities, so neither can absorb the other. The
// replay must leave the exact notification rows, the terminal-effect ledger, the
// failure counter and the single termination publish intent byte-identical.
func TestIntegrationTerminalFailureReplayKeepsDurableEffects(t *testing.T) {
	fixture := durableTerminalSeed(t, 2)
	ctx := t.Context()

	firstJob := durableTerminalJobID(fixture.tag, "terminal-replay-first-failure")
	thresholdJob := durableTerminalJobID(fixture.tag, "terminal-replay-threshold-failure")

	if err := fixture.repo.handleJobFailed(ctx, durableTerminalJobFailedEvent(fixture, firstJob)); err != nil {
		t.Fatalf("handleJobFailed(first failure) = %v", err)
	}
	if err := fixture.repo.handleJobFailed(ctx, durableTerminalJobFailedEvent(fixture, thresholdJob)); err != nil {
		t.Fatalf("handleJobFailed(threshold failure) = %v", err)
	}

	notifications := durableTerminalNotifications(ctx, t, fixture.pg, fixture.userID)
	if len(notifications) != 3 {
		t.Fatalf("durable notifications = %d (%+v), want 3 (two job failures and one termination)", len(notifications), notifications)
	}
	durableTerminalAssertCounter(ctx, t, fixture, 2)

	// The job failure and the workflow termination are separate deterministic
	// identities, so a replay cannot silently reuse one row for the other.
	failure := durableTerminalNotificationByKey(t, notifications, idempotency.JobNotificationEventKey(thresholdJob, durableTerminalJobFailedTitle))
	termination := durableTerminalNotificationByKey(t, notifications, idempotency.WorkflowNotificationEventKey(fixture.workflowID, durableTerminalTerminatedTitle, thresholdJob))
	if failure.ID == termination.ID {
		t.Fatalf("job failure and workflow termination shared durable notification %q", failure.ID)
	}
	durableTerminalAssertKind(t, failure, notificationsmodel.KindWebError.ToString())
	durableTerminalAssertKind(t, termination, notificationsmodel.KindWebAlert.ToString())

	effects := durableTerminalEffects(ctx, t, fixture.pg, fixture.workflowID)
	if len(effects) != 2 {
		t.Fatalf("terminal effect ledger = %+v, want one FAILED row per failed job", effects)
	}
	if terminates := durableTerminalTerminateEvents(ctx, t, fixture.pg, fixture.workflowID); terminates != 1 {
		t.Fatalf("termination outbox events = %d, want 1", terminates)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		if err := fixture.repo.handleJobFailed(ctx, durableTerminalJobFailedEvent(fixture, thresholdJob)); err != nil {
			t.Fatalf("handleJobFailed(replay %d) = %v", attempt, err)
		}
	}

	durableTerminalAssertNotifications(t, notifications, durableTerminalNotifications(ctx, t, fixture.pg, fixture.userID))
	durableTerminalAssertEffects(t, effects, durableTerminalEffects(ctx, t, fixture.pg, fixture.workflowID))
	durableTerminalAssertCounter(ctx, t, fixture, 2)
	if terminates := durableTerminalTerminateEvents(ctx, t, fixture.pg, fixture.workflowID); terminates != 1 {
		t.Fatalf("termination outbox events after replay = %d, want 1", terminates)
	}
}

// TestIntegrationTerminalCompletionReplayKeepsNewerFailureCount proves a
// redelivered JOB_COMPLETED event cannot rewind state. The completion reset
// already happened, so a later failure is a new fact the replay must not erase,
// and the completion notification must not be published a second time.
func TestIntegrationTerminalCompletionReplayKeepsNewerFailureCount(t *testing.T) {
	fixture := durableTerminalSeed(t, 2)
	ctx := t.Context()

	failedJob := durableTerminalJobID(fixture.tag, "completion-replay-first-failure")
	completedJob := durableTerminalJobID(fixture.tag, "completion-replay-completing-job")
	laterFailedJob := durableTerminalJobID(fixture.tag, "completion-replay-later-failure")

	if err := fixture.repo.handleJobFailed(ctx, durableTerminalJobFailedEvent(fixture, failedJob)); err != nil {
		t.Fatalf("handleJobFailed(first) = %v", err)
	}
	if err := fixture.repo.handleJobCompleted(ctx, durableTerminalJobCompletedEvent(fixture, completedJob)); err != nil {
		t.Fatalf("handleJobCompleted = %v", err)
	}
	if err := fixture.repo.handleJobFailed(ctx, durableTerminalJobFailedEvent(fixture, laterFailedJob)); err != nil {
		t.Fatalf("handleJobFailed(later) = %v", err)
	}
	durableTerminalAssertCounter(ctx, t, fixture, 1)

	notifications := durableTerminalNotifications(ctx, t, fixture.pg, fixture.userID)
	if len(notifications) != 3 {
		t.Fatalf("durable notifications = %d (%+v), want 3 (two failures and one completion)", len(notifications), notifications)
	}
	completion := durableTerminalNotificationByKey(t, notifications, idempotency.JobNotificationEventKey(completedJob, durableTerminalJobCompletedTitle))
	durableTerminalAssertKind(t, completion, notificationsmodel.KindWebSuccess.ToString())
	effects := durableTerminalEffects(ctx, t, fixture.pg, fixture.workflowID)
	durableTerminalEffectByJob(t, effects, completedJob, "COMPLETED")

	for attempt := 1; attempt <= 2; attempt++ {
		if err := fixture.repo.handleJobCompleted(ctx, durableTerminalJobCompletedEvent(fixture, completedJob)); err != nil {
			t.Fatalf("handleJobCompleted(replay %d) = %v", attempt, err)
		}
	}

	durableTerminalAssertNotifications(t, notifications, durableTerminalNotifications(ctx, t, fixture.pg, fixture.userID))
	durableTerminalAssertEffects(t, effects, durableTerminalEffects(ctx, t, fixture.pg, fixture.workflowID))
	// The replay must not clear the failure recorded after the completion.
	durableTerminalAssertCounter(ctx, t, fixture, 1)
	if terminates := durableTerminalTerminateEvents(ctx, t, fixture.pg, fixture.workflowID); terminates != 0 {
		t.Fatalf("termination outbox events = %d, want 0 (the threshold was never reached)", terminates)
	}
}

// TestIntegrationTerminalFailureReplayAfterPartialPublicationPersistsRemainingAlert
// proves the AlreadyExists tolerance in sendNotification cannot swallow a distinct
// effect that a mid-handler failure left unpublished. The first run commits the
// failure counter, the terminal-effect ledger and the termination publish intent
// plus the job notification, then loses the notification RPC before the alert is
// stored. The replay of the same threshold event carries a drifted display name,
// so the already-stored job notification conflicts with the rebuilt payload and
// must be left untouched, while the missing alert must still be published - once.
func TestIntegrationTerminalFailureReplayAfterPartialPublicationPersistsRemainingAlert(t *testing.T) {
	fixture := durableTerminalSeed(t, 1)
	ctx := t.Context()

	thresholdJob := durableTerminalJobID(fixture.tag, "partial-publication-threshold-job")

	// Lose exactly one alert publish. Everything else, including every later
	// call, is served by the actual notifications repository.
	fixture.repo = durableTerminalRepository(fixture.pg, &durableTerminalFailFirstAlertNotificationsClient{
		inner:     fixture.notifications,
		failAlert: true,
	})

	// max=1, so the first failure is already the threshold failure.
	firstErr := fixture.repo.handleJobFailed(ctx, durableTerminalJobFailedEvent(fixture, thresholdJob))
	if code := status.Code(firstErr); code != codes.Unavailable {
		t.Fatalf("handleJobFailed(first) code = %s, want %s (err: %v)", code, codes.Unavailable, firstErr)
	}

	// The handler failed only at the alert step, so everything before it is committed.
	durableTerminalAssertCounter(ctx, t, fixture, 1)
	afterFailure := durableTerminalNotifications(ctx, t, fixture.pg, fixture.userID)
	published := durableTerminalNotificationByKey(t, afterFailure, idempotency.JobNotificationEventKey(thresholdJob, durableTerminalJobFailedTitle))
	durableTerminalAssertNoNotificationKey(t, afterFailure, idempotency.WorkflowNotificationEventKey(fixture.workflowID, durableTerminalTerminatedTitle, thresholdJob))
	durableTerminalAssertEffects(t,
		[]durableTerminalEffectRow{{JobID: thresholdJob, Effect: "FAILED", ThresholdReached: sql.NullBool{Bool: true, Valid: true}}},
		durableTerminalEffects(ctx, t, fixture.pg, fixture.workflowID),
	)
	if terminates := durableTerminalTerminateEvents(ctx, t, fixture.pg, fixture.workflowID); terminates != 1 {
		t.Fatalf("termination outbox events after the failed run = %d, want 1", terminates)
	}

	// Mutable display data changes after the job notification was published but
	// before the alert ever was.
	renamed := "renamed-" + fixture.tag
	if _, err := fixture.pg.Exec(ctx, `UPDATE workflows SET name = $2 WHERE id = $1`, fixture.workflowID, renamed); err != nil {
		t.Fatalf("rename fixture workflow: %v", err)
	}
	// Read the rename back through the repository the handlers use, so the drift
	// is proven real rather than assumed.
	stored, err := fixture.workflows.GetWorkflowByID(ctx, fixture.workflowID)
	if err != nil {
		t.Fatalf("GetWorkflowByID after rename: %v", err)
	}
	if stored.Name != renamed {
		t.Fatalf("stored workflow name = %q, want the drifted %q", stored.Name, renamed)
	}

	if err := fixture.repo.handleJobFailed(ctx, durableTerminalJobFailedEvent(fixture, thresholdJob)); err != nil {
		t.Fatalf("handleJobFailed(replay) = %v", err)
	}

	replayed := durableTerminalNotifications(ctx, t, fixture.pg, fixture.userID)
	if len(replayed) != 2 {
		t.Fatalf("durable notifications after the replay = %+v, want the stored job failure and the newly published alert", replayed)
	}
	// The effect the failed run completed is not rewritten by the drifted payload.
	kept := durableTerminalNotificationByKey(t, replayed, idempotency.JobNotificationEventKey(thresholdJob, durableTerminalJobFailedTitle))
	if kept != published {
		t.Fatalf("drifted replay rewrote the completed job effect: %+v, want %+v", kept, published)
	}
	// The effect the failed run never stored is published now, exactly once.
	alert := durableTerminalNotificationByKey(t, replayed, idempotency.WorkflowNotificationEventKey(fixture.workflowID, durableTerminalTerminatedTitle, thresholdJob))
	durableTerminalAssertKind(t, alert, notificationsmodel.KindWebAlert.ToString())
	if message := durableTerminalMessage(t, alert.Payload); !strings.Contains(message, renamed) {
		t.Fatalf("alert message %q does not carry the current display name %q", message, renamed)
	}
	durableTerminalAssertCounter(ctx, t, fixture, 1)
	if terminates := durableTerminalTerminateEvents(ctx, t, fixture.pg, fixture.workflowID); terminates != 1 {
		t.Fatalf("termination outbox events after the replay = %d, want 1", terminates)
	}

	// Replaying once more is a pure no-op.
	if err := fixture.repo.handleJobFailed(ctx, durableTerminalJobFailedEvent(fixture, thresholdJob)); err != nil {
		t.Fatalf("handleJobFailed(second replay) = %v", err)
	}

	durableTerminalAssertNotifications(t, replayed, durableTerminalNotifications(ctx, t, fixture.pg, fixture.userID))
	durableTerminalAssertCounter(ctx, t, fixture, 1)
}

// TestIntegrationTerminalReplayAfterTerminationAndDeletionWritesNothing proves
// terminal events cannot resurrect a workflow that has already left service. A
// failure the terminated workflow never acknowledged is still reported under that
// job's own identity, because the job really did fail, but it must not advance
// the failure counter or publish a second termination. After deletion the same
// event must not recreate any durable row.
func TestIntegrationTerminalReplayAfterTerminationAndDeletionWritesNothing(t *testing.T) {
	fixture := durableTerminalSeed(t, 1)
	ctx := t.Context()

	thresholdJob := durableTerminalJobID(fixture.tag, "terminated-threshold-job")
	lateJob := durableTerminalJobID(fixture.tag, "terminated-late-job")

	if err := fixture.repo.handleJobFailed(ctx, durableTerminalJobFailedEvent(fixture, thresholdJob)); err != nil {
		t.Fatalf("handleJobFailed(threshold) = %v", err)
	}
	durableTerminalAssertCounter(ctx, t, fixture, 1)

	notifications := durableTerminalNotifications(ctx, t, fixture.pg, fixture.userID)
	effects := durableTerminalEffects(ctx, t, fixture.pg, fixture.workflowID)
	if len(notifications) != 2 || len(effects) != 1 {
		t.Fatalf("terminal publication = %d notifications / %d ledger rows, want 2 / 1", len(notifications), len(effects))
	}

	if err := fixture.repo.handleJobFailed(ctx, durableTerminalJobFailedEvent(fixture, lateJob)); err != nil {
		t.Fatalf("handleJobFailed(late job) = %v", err)
	}
	durableTerminalAssertCounter(ctx, t, fixture, 1)
	if terminates := durableTerminalTerminateEvents(ctx, t, fixture.pg, fixture.workflowID); terminates != 1 {
		t.Fatalf("termination outbox events after a late failure = %d, want 1", terminates)
	}
	afterLateFailure := durableTerminalNotifications(ctx, t, fixture.pg, fixture.userID)
	if len(afterLateFailure) != len(notifications)+1 {
		t.Fatalf("durable notifications after a late failure = %+v, want exactly one new job failure", afterLateFailure)
	}
	durableTerminalNotificationByKey(t, afterLateFailure, idempotency.JobNotificationEventKey(lateJob, durableTerminalJobFailedTitle))
	durableTerminalAssertNoNotificationKey(t, afterLateFailure, idempotency.WorkflowNotificationEventKey(fixture.workflowID, durableTerminalTerminatedTitle, lateJob))

	if err := fixture.workflows.DeleteWorkflow(ctx, fixture.workflowID, fixture.userID); err != nil {
		t.Fatalf("DeleteWorkflow: %v", err)
	}
	if err := fixture.repo.handleJobFailed(ctx, durableTerminalJobFailedEvent(fixture, thresholdJob)); err != nil {
		t.Fatalf("handleJobFailed(replay after delete) = %v", err)
	}

	durableTerminalAssertNotifications(t, afterLateFailure, durableTerminalNotifications(ctx, t, fixture.pg, fixture.userID))
	if remaining := durableTerminalEffects(ctx, t, fixture.pg, fixture.workflowID); len(remaining) != 0 {
		t.Fatalf("terminal effect ledger after delete = %+v, want no rows", remaining)
	}
}

// durableTerminalWorkflowsClient adapts the exported workflows repository to the
// internal Workflows RPC surface the terminal handlers call. Every call is
// forwarded verbatim, so the failure counter, the terminal-effect ledger and the
// termination publish intent are all produced by production code.
type durableTerminalWorkflowsClient struct {
	workflowspb.WorkflowsServiceClient
	repo *workflows.Repository
}

func (c durableTerminalWorkflowsClient) GetWorkflowByID(ctx context.Context, req *workflowspb.GetWorkflowByIDRequest, _ ...grpc.CallOption) (*workflowspb.GetWorkflowByIDResponse, error) {
	res, err := c.repo.GetWorkflowByID(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return res.ToProto(), nil
}

func (c durableTerminalWorkflowsClient) IncrementWorkflowConsecutiveJobFailuresCount(
	ctx context.Context,
	req *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest,
	_ ...grpc.CallOption,
) (*workflowspb.IncrementWorkflowConsecutiveJobFailuresCountResponse, error) {
	thresholdReached, err := c.repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, req.GetId(), req.GetUserId(), req.GetJobId())
	if err != nil {
		return nil, err
	}
	return &workflowspb.IncrementWorkflowConsecutiveJobFailuresCountResponse{ThresholdReached: thresholdReached}, nil
}

func (c durableTerminalWorkflowsClient) ResetWorkflowConsecutiveJobFailuresCount(
	ctx context.Context,
	req *workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest,
	_ ...grpc.CallOption,
) (*workflowspb.ResetWorkflowConsecutiveJobFailuresCountResponse, error) {
	if err := c.repo.ResetWorkflowConsecutiveJobFailuresCount(ctx, req.GetId(), req.GetUserId(), req.GetJobId()); err != nil {
		return nil, err
	}
	return &workflowspb.ResetWorkflowConsecutiveJobFailuresCountResponse{}, nil
}

// durableTerminalNotificationsClient adapts the exported notifications
// repository to the Notifications RPC surface the terminal handlers call. The
// repository performs the deduplication, so no test-side stub can decide what a
// replay publishes.
type durableTerminalNotificationsClient struct {
	notificationspb.NotificationsServiceClient
	repo *notifications.Repository
}

func (c durableTerminalNotificationsClient) CreateNotification(
	ctx context.Context,
	req *notificationspb.CreateNotificationRequest,
	_ ...grpc.CallOption,
) (*notificationspb.CreateNotificationResponse, error) {
	notificationID, err := c.repo.CreateNotification(ctx, req.GetUserId(), req.GetKind(), req.GetPayload(), req.GetIdempotencyKey())
	if err != nil {
		return nil, err
	}
	return &notificationspb.CreateNotificationResponse{Id: notificationID}, nil
}

// durableTerminalFailFirstAlertNotificationsClient wraps the real notifications
// adapter and loses the first Web-alert publish, so the handler must be observed
// failing *after* it already committed its counter, ledger and termination
// intent. Every other call, and every later call, still reaches the actual
// notifications repository: nothing about persistence or deduplication is faked.
type durableTerminalFailFirstAlertNotificationsClient struct {
	notificationspb.NotificationsServiceClient
	inner     durableTerminalNotificationsClient
	failAlert bool
}

func (c *durableTerminalFailFirstAlertNotificationsClient) CreateNotification(
	ctx context.Context,
	req *notificationspb.CreateNotificationRequest,
	opts ...grpc.CallOption,
) (*notificationspb.CreateNotificationResponse, error) {
	if c.failAlert && req.GetKind() == notificationsmodel.KindWebAlert.ToString() {
		c.failAlert = false
		return nil, status.Error(codes.Unavailable, "notifications service unavailable")
	}
	return c.inner.CreateNotification(ctx, req, opts...)
}

// durableTerminalRepository wires the terminal handlers to the real workflows
// repository and the given notifications surface over the shared PostgreSQL
// pool. Tests that need to inject an RPC failure pass a wrapper around the real
// adapter; everything else passes the adapter itself.
func durableTerminalRepository(pg *postgres.Postgres, client notificationspb.NotificationsServiceClient) *Repository {
	return &Repository{
		auth: testAuth{},
		svc: &Services{
			Workflows: durableTerminalWorkflowsClient{
				repo: workflows.New(&workflows.Config{FetchLimit: 20}, pg),
			},
			Notifications: client,
		},
	}
}

// durableTerminalTag returns a short unique identity fragment. Fixtures never
// derive identity from t.Name(), whose subtest slashes would both overflow the
// users.email column and collide across reruns.
func durableTerminalTag() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}

// durableTerminalJobID returns a unique job identity for one terminal event. The
// fixture tag is part of the input, so separate fixtures and separate runs never
// share a job identity, while a replay inside one test keeps reusing the value
// the test already captured. The workflows repository canonicalizes job ids
// before writing the terminal-effect ledger, so every event needs a real UUID.
func durableTerminalJobID(tag, label string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("chronoverse-durable-terminal:"+tag+":"+label)).String()
}

// durableTerminalSeed creates one unique user, one built workflow owned by that
// user and the handler wiring, and registers a cleanup that deletes exactly
// those rows even when the test aborts.
func durableTerminalSeed(t *testing.T, maxConsecutiveFailures int32) *durableTerminalFixture {
	t.Helper()

	ctx := t.Context()
	pg := testkit.Postgres(t)
	tag := durableTerminalTag()
	userID := testkit.SeedUser(ctx, t, pg, "durable-terminal-"+tag+"@chronoverse.test")

	workflowsRepo := workflows.New(&workflows.Config{FetchLimit: 20}, pg)
	name := "durable-terminal-" + tag
	created, err := workflowsRepo.CreateWorkflow(
		ctx, userID, name, `{"image":"alpine:3.22.2"}`,
		workflowsmodel.KindContainer.ToString(), 60, maxConsecutiveFailures, true,
		"durable-terminal-create-"+tag,
	)
	if err != nil {
		durableTerminalCleanup(t, pg, userID)
		t.Fatalf("CreateWorkflow: %v", err)
	}
	t.Cleanup(func() { durableTerminalCleanup(t, pg, userID) })
	// Drive the workflow through the public build transitions so the terminal
	// handlers read a fully built workflow instead of a queued one.
	for _, step := range []struct {
		status string
		ref    string
		digest string
	}{
		{status: workflowsmodel.WorkflowBuildStatusStarted.ToString()},
		{status: workflowsmodel.WorkflowBuildStatusCompleted.ToString(), ref: "alpine:3.22.2", digest: "sha256:durable-terminal"},
	} {
		if buildErr := workflowsRepo.UpdateWorkflowBuildStatus(ctx, created.ID, userID, step.status, created.Generation, step.ref, step.digest); buildErr != nil {
			t.Fatalf("UpdateWorkflowBuildStatus(%s): %v", step.status, buildErr)
		}
	}

	notificationsClient := durableTerminalNotificationsClient{
		repo: notifications.New(&notifications.Config{FetchLimit: 20}, testAuth{}, pg, &notifications.Services{}),
	}

	return &durableTerminalFixture{
		pg:            pg,
		repo:          durableTerminalRepository(pg, notificationsClient),
		workflows:     workflowsRepo,
		notifications: notificationsClient,
		tag:           tag,
		name:          name,
		userID:        userID,
		workflowID:    created.ID,
	}
}

// durableTerminalCleanup deletes only the rows this fixture created. It runs on a
// context detached from the test so an aborted or finished test still tidies up,
// and every statement is bounded so a stuck container cannot hang the suite.
func durableTerminalCleanup(t *testing.T, pg *postgres.Postgres, userID string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), durableTerminalCleanupTimeout)
	defer cancel()

	statements := []struct {
		query string
		args  []any
	}{
		{fmt.Sprintf("DELETE FROM %s WHERE user_id = $1", postgres.TableNotifications), []any{userID}},
		{fmt.Sprintf("DELETE FROM %s WHERE user_id = $1", postgres.TableWorkflowTerminalEffects), []any{userID}},
		{fmt.Sprintf("DELETE FROM %s WHERE scope = $1", postgres.TableCommandIdempotencyKeys), []any{commandidempotency.UserScope(userID)}},
		// Outbox rows carry no user column, so they are removed by the workflow
		// publish identity, which only this fixture's user ever writes.
		{fmt.Sprintf("DELETE FROM %s WHERE topic = $1 AND payload->>'UserID' = $2", postgres.TableOutboxEvents), []any{kafkapkg.TopicWorkflows, userID}},
		{fmt.Sprintf("DELETE FROM %s WHERE user_id = $1", postgres.TableWorkflows), []any{userID}},
		{fmt.Sprintf("DELETE FROM %s WHERE id = $1", postgres.TableUsers), []any{userID}},
	}
	for _, statement := range statements {
		if _, err := pg.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Errorf("cleanup fixture user %q: %v", userID, err)
			return
		}
	}
}

// durableTerminalJobFailedEvent and durableTerminalJobCompletedEvent rebuild the
// exact workflow events the jobs worker publishes for a terminal job, so the
// handlers run against the same identities production redelivers.
func durableTerminalJobFailedEvent(fixture *durableTerminalFixture, jobID string) *workflowsmodel.WorkflowEvent {
	return &workflowsmodel.WorkflowEvent{
		EventKey: idempotency.JobWorkflowEventKey(jobID, workflowsmodel.ActionJobFailed.ToString()),
		ID:       fixture.workflowID,
		UserID:   fixture.userID,
		Action:   workflowsmodel.ActionJobFailed,
		JobID:    jobID,
	}
}

func durableTerminalJobCompletedEvent(fixture *durableTerminalFixture, jobID string) *workflowsmodel.WorkflowEvent {
	return &workflowsmodel.WorkflowEvent{
		EventKey: idempotency.JobWorkflowEventKey(jobID, workflowsmodel.ActionJobCompleted.ToString()),
		ID:       fixture.workflowID,
		UserID:   fixture.userID,
		Action:   workflowsmodel.ActionJobCompleted,
		JobID:    jobID,
	}
}

// durableTerminalNotifications reads every durable notification row the fixture
// user owns, so an unexpected effect cannot hide behind a narrow key allowlist.
func durableTerminalNotifications(ctx context.Context, t *testing.T, pg *postgres.Postgres, userID string) []durableTerminalNotificationRow {
	t.Helper()

	rows, err := pg.Query(ctx, `
		SELECT id::text, kind, payload::text, idempotency_key
		FROM notifications
		WHERE user_id = $1
		ORDER BY created_at, id
	`, userID)
	if err != nil {
		t.Fatalf("read durable notifications: %v", err)
	}
	defer rows.Close()

	read := []durableTerminalNotificationRow{}
	for rows.Next() {
		var row durableTerminalNotificationRow
		if scanErr := rows.Scan(&row.ID, &row.Kind, &row.Payload, &row.IdempotencyKey); scanErr != nil {
			t.Fatalf("scan durable notification: %v", scanErr)
		}
		read = append(read, row)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("iterate durable notifications: %v", rowsErr)
	}
	return read
}

// durableTerminalEffects reads the terminal-effect ledger rows the fixture
// workflow owns, which is what makes a repeated failure a replay instead of a
// second counted failure.
func durableTerminalEffects(ctx context.Context, t *testing.T, pg *postgres.Postgres, workflowID string) []durableTerminalEffectRow {
	t.Helper()

	rows, err := pg.Query(ctx, `
		SELECT job_id::text, effect, threshold_reached
		FROM workflow_terminal_effects
		WHERE workflow_id = $1
		ORDER BY job_id
	`, workflowID)
	if err != nil {
		t.Fatalf("read terminal effect ledger: %v", err)
	}
	defer rows.Close()

	read := []durableTerminalEffectRow{}
	for rows.Next() {
		var row durableTerminalEffectRow
		if scanErr := rows.Scan(&row.JobID, &row.Effect, &row.ThresholdReached); scanErr != nil {
			t.Fatalf("scan terminal effect ledger row: %v", scanErr)
		}
		read = append(read, row)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("iterate terminal effect ledger: %v", rowsErr)
	}
	return read
}

// durableTerminalTerminateEvents counts the durable termination publish intents
// the fixture workflow owns, by the same action the workflows repository writes.
func durableTerminalTerminateEvents(ctx context.Context, t *testing.T, pg *postgres.Postgres, workflowID string) int {
	t.Helper()

	var count int
	if err := pg.QueryRow(ctx, `
		SELECT count(*)
		FROM outbox_events
		WHERE topic = $1 AND kafka_key = $2 AND payload->>'Action' = $3
	`, kafkapkg.TopicWorkflows, workflowID, workflowsmodel.ActionTerminate.ToString()).Scan(&count); err != nil {
		t.Fatalf("count termination outbox events: %v", err)
	}
	return count
}

func durableTerminalAssertCounter(ctx context.Context, t *testing.T, fixture *durableTerminalFixture, want int32) {
	t.Helper()

	var got int32
	if err := fixture.pg.QueryRow(ctx, `
		SELECT consecutive_job_failures_count FROM workflows WHERE id = $1
	`, fixture.workflowID).Scan(&got); err != nil {
		t.Fatalf("read consecutive_job_failures_count: %v", err)
	}
	if got != want {
		t.Fatalf("consecutive_job_failures_count = %d, want %d", got, want)
	}
}

func durableTerminalAssertNotifications(t *testing.T, want, got []durableTerminalNotificationRow) {
	t.Helper()

	if !reflect.DeepEqual(want, got) {
		t.Fatalf("durable notification rows = %+v, want unchanged %+v", got, want)
	}
}

func durableTerminalAssertEffects(t *testing.T, want, got []durableTerminalEffectRow) {
	t.Helper()

	if !reflect.DeepEqual(want, got) {
		t.Fatalf("terminal effect ledger = %+v, want unchanged %+v", got, want)
	}
}

func durableTerminalAssertKind(t *testing.T, row durableTerminalNotificationRow, want string) {
	t.Helper()

	if row.Kind != want {
		t.Fatalf("notification %q kind = %q, want %q", row.ID, row.Kind, want)
	}
}

// durableTerminalNotificationByKey returns the durable row a deterministic
// idempotency key produced, proving the identity the handler derives is exactly
// what was persisted.
func durableTerminalNotificationByKey(t *testing.T, rows []durableTerminalNotificationRow, key string) durableTerminalNotificationRow {
	t.Helper()

	for _, row := range rows {
		if row.IdempotencyKey == key {
			return row
		}
	}
	t.Fatalf("no durable notification for key %q in %+v", key, rows)
	return durableTerminalNotificationRow{}
}

// durableTerminalAssertNoNotificationKey proves an effect that was never
// published stays absent, so tolerating one durable effect cannot be mistaken for
// suppressing the remaining ones.
func durableTerminalAssertNoNotificationKey(t *testing.T, rows []durableTerminalNotificationRow, key string) {
	t.Helper()

	for _, row := range rows {
		if row.IdempotencyKey == key {
			t.Fatalf("unexpected durable notification %+v for key %q", row, key)
		}
	}
}

// durableTerminalEffectByJob returns the ledger row one job's terminal effect
// produced, so a test can assert the recorded effect rather than a row count.
func durableTerminalEffectByJob(t *testing.T, rows []durableTerminalEffectRow, jobID, wantEffect string) durableTerminalEffectRow {
	t.Helper()

	for _, row := range rows {
		if row.JobID == jobID {
			if row.Effect != wantEffect {
				t.Fatalf("terminal effect for job %q = %q, want %q", jobID, row.Effect, wantEffect)
			}
			return row
		}
	}
	t.Fatalf("no terminal effect for job %q in %+v", jobID, rows)
	return durableTerminalEffectRow{}
}

func durableTerminalMessage(t *testing.T, payload string) string {
	t.Helper()

	var fields map[string]string
	if err := json.Unmarshal([]byte(payload), &fields); err != nil {
		t.Fatalf("unmarshal notification payload: %v", err)
	}
	return fields["message"]
}
