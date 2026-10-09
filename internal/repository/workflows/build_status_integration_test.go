//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package workflows

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

// heartbeatFixturePayload is the HEARTBEAT payload fixture workflows are created
// with. A heartbeat resolves no image, so it is the kind whose build results must
// carry no resolved image identity at all.
const heartbeatFixturePayload = `{"endpoint":"https://chronoverse.test/health","timeout":"10s"}`

// serverSideCancellation is how PostgreSQL words a statement it canceled itself,
// as pg_cancel_backend does. It is the only text that distinguishes a database
// failure from a caller that went away once both are reported as internal faults.
const serverSideCancellation = "canceling statement"

// buildStatusStep is one build result delivered for a fixture generation.
type buildStatusStep struct {
	status string
	ref    string
	digest string
}

// seedWorkflowFixtureOfKind creates a fixture user and one workflow of an
// explicit kind. The build state machine allows each kind through different
// states, so the cases that prove a transition is allowed or refused need one
// fixture of each kind rather than the container fixture everything else uses.
func seedWorkflowFixtureOfKind(
	ctx context.Context,
	t *testing.T,
	pg *postgres.Postgres,
	repo *Repository,
	kind,
	payload string,
) *workflowFixture {
	t.Helper()

	const maxFailures = int32(3)
	userID := testkit.SeedUser(ctx, t, pg, "cv-"+fixtureTag()+"@chronoverse.test")
	registerFixtureCleanup(ctx, t, pg, userID)

	return createFixtureWorkflow(ctx, t, repo, userID, kind, payload, maxFailures)
}

// assertBuildStatusCode reports the code a build result was refused with without
// ending the case, so the durable-state checks that follow still run and report
// what the command actually left behind. assertCode is fatal, and a fatal code
// assertion would hide that evidence from exactly the failures it is most useful
// for: a command that both reports the wrong code and wrote something.
func assertBuildStatusCode(t *testing.T, name string, err error, want codes.Code) {
	t.Helper()

	if got := status.Code(err); got != want {
		t.Errorf("%s code = %v, want %v (err: %v)", name, got, want, err)
	}
}

// lockBuildStatusStateRead stops a build result at the row lock its own build
// state read takes. The pattern names that read's projection, so a probe cannot
// mistake another command's read of the same row for it.
func lockBuildStatusStateRead(fixture *workflowFixture) interruptionLock {
	return lockWorkflowRow(fixture.WorkflowID, "%SELECT kind, build_status, generation%")
}

// lockBuildStatusUpdate stops a build result at the statement that records it.
// SHARE ROW EXCLUSIVE lets the state read through and stops the UPDATE, which a
// row lock cannot do because it would stop the read first.
func lockBuildStatusUpdate(*workflowFixture) interruptionLock {
	return lockWorkflowWrite("%UPDATE " + postgres.TableWorkflows + "%SET build_status%")
}

// TestIntegrationUpdateWorkflowBuildStatusReportsAMalformedIdentityAsAServerFault
// pins what a caller that sent a malformed identity actually receives. The
// service layer checks only that the field is present, so the id reaches the
// command as given, and the command hands it to PostgreSQL unvalidated. Its state
// read then classifies every failure that is not a missing row as an internal
// one, so a malformed identity is answered with the server-fault code rather than
// the invalid-argument code the other mutation paths return for the same input.
//
// The case exists to pin what a caller receives, not to endorse it: a caller that
// sent a malformed id is told the server faulted, which is the wrong answer, and
// this assertion will change when the command classifies the failure the way its
// siblings do.
func TestIntegrationUpdateWorkflowBuildStatusReportsAMalformedIdentityAsAServerFault(t *testing.T) {
	const started = "STARTED"

	for _, tc := range []struct {
		name       string
		workflowID func(*workflowFixture) string
		userID     func(*workflowFixture) string
	}{
		{
			name:       "MalformedWorkflowID",
			workflowID: func(*workflowFixture) string { return "not-a-uuid" },
			userID:     func(f *workflowFixture) string { return f.UserID },
		},
		{
			name:       "MalformedUserID",
			workflowID: func(f *workflowFixture) string { return f.WorkflowID },
			userID:     func(*workflowFixture) string { return "not-a-uuid" },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			pg := testkit.Postgres(t)
			repo := newTestRepository(t)

			fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
			before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
			eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)

			err := repo.UpdateWorkflowBuildStatus(
				ctx, tc.workflowID(fixture), tc.userID(fixture), started, fixture.Generation, "", "",
			)
			assertBuildStatusCode(t, tc.name, err, codes.Internal)
			if message := status.Convert(err).Message(); !strings.Contains(message, "failed to read workflow build state") {
				t.Errorf("%s message = %q, want it to name the state read that refused", tc.name, message)
			}
			assertBuildStateUndone(ctx, t, pg, fixture, before, eventsBefore, tc.name)
		})
	}
}

// TestIntegrationUpdateWorkflowBuildStatusReportsEachOutcome proves the build
// result path classifies its failures, and applies none of itself when it
// cannot classify one. A build result takes no idempotency key and publishes
// nothing, so what each case has to leave behind is the workflow's own build
// state.
//
// The three interruption shapes are distinct outcomes rather than variations of
// one: an explicit cancellation and an expired deadline are the caller's own
// failure, while a cancellation issued inside PostgreSQL is a database failure
// that no caller-side stop can produce.
func TestIntegrationUpdateWorkflowBuildStatusReportsEachOutcome(t *testing.T) {
	const missingWorkflow = "workflow not found"

	// The two statements the command runs classify their failures differently,
	// which is why every case states its own code and cause rather than deriving
	// them from the interruption. The statement that records the result tells a
	// caller that went away or ran out of time apart from a database failure,
	// while the build state read ahead of it classifies only a missing row and
	// reports every other failure to read it as an internal one naming the
	// statement. Both shapes are stated here so the difference is pinned rather
	// than assumed.
	started := workflowsmodel.WorkflowBuildStatusStarted.ToString()
	for _, tc := range []struct {
		name string
		lock func(*workflowFixture) interruptionLock
		// useDeadline gives the caller its own expiring deadline instead of an
		// explicit cancellation.
		useDeadline bool
		// cancelInsidePostgres cancels the statement inside PostgreSQL rather than
		// the caller's own context, which is the only interruption that reaches the
		// internal arm.
		cancelInsidePostgres bool
		wantCode             codes.Code
		// wantCause is the underlying outcome the returned message has to name.
		// Every case asserts it rather than the statement the command was running,
		// because the statement prefix is the same format string on every failure
		// that reaches one arm and so cannot tell two causes apart.
		wantCause string
	}{
		{
			name:      "WhileReadingTheBuildState/CanceledCaller",
			lock:      lockBuildStatusStateRead,
			wantCode:  codes.Internal,
			wantCause: context.Canceled.Error(),
		},
		{
			name:        "WhileReadingTheBuildState/ExpiredDeadline",
			lock:        lockBuildStatusStateRead,
			useDeadline: true,
			wantCode:    codes.Internal,
			wantCause:   context.DeadlineExceeded.Error(),
		},
		{
			name:                 "WhileReadingTheBuildState/CanceledInsidePostgres",
			lock:                 lockBuildStatusStateRead,
			cancelInsidePostgres: true,
			wantCode:             codes.Internal,
			wantCause:            serverSideCancellation,
		},
		{
			name:      "WhileRecordingTheResult/CanceledCaller",
			lock:      lockBuildStatusUpdate,
			wantCode:  codes.Canceled,
			wantCause: context.Canceled.Error(),
		},
		{
			// A deadline is the caller's own failure too, and a retry signal rather
			// than a permanent one, so the write has to distinguish it from an
			// explicit cancellation.
			name:        "WhileRecordingTheResult/ExpiredDeadline",
			lock:        lockBuildStatusUpdate,
			useDeadline: true,
			wantCode:    codes.DeadlineExceeded,
			wantCause:   context.DeadlineExceeded.Error(),
		},
		{
			name:                 "WhileRecordingTheResult/CanceledInsidePostgres",
			lock:                 lockBuildStatusUpdate,
			cancelInsidePostgres: true,
			wantCode:             codes.Internal,
			wantCause:            serverSideCancellation,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			pg := testkit.Postgres(t)
			repo := newTestRepository(t)

			fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
			before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
			eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)

			lock := tc.lock(fixture)
			callerCtx := ctx
			step := blockedStep(cancelCaller)
			switch {
			case tc.useDeadline:
				var cancelDeadline context.CancelFunc
				callerCtx, cancelDeadline = context.WithTimeout(ctx, callerDeadlineDelay)
				t.Cleanup(cancelDeadline)
				step = letCallerDeadlineExpire
			case tc.cancelInsidePostgres:
				step = func(pgx.Tx, context.CancelFunc) { cancelBlockedStatement(ctx, t, pg, lock) }
			}

			err := runWhileBlocked(
				callerCtx, t, pg, lock, step,
				func(commandCtx context.Context) error {
					return repo.UpdateWorkflowBuildStatus(
						commandCtx, fixture.WorkflowID, fixture.UserID, started, fixture.Generation, "", "",
					)
				},
			)
			assertBuildStatusCode(t, tc.name, err, tc.wantCode)
			if message := status.Convert(err).Message(); !strings.Contains(message, tc.wantCause) {
				t.Errorf("%s message = %q, want it to name %q", tc.name, message, tc.wantCause)
			}
			assertNotRefusedAsMissing(t, tc.name, err, missingWorkflow, "workflow generation mismatch")
			assertBuildStateUndone(ctx, t, pg, fixture, before, eventsBefore, tc.name)
		})
	}

	// A caller that is already gone fails at the first statement it issues. This
	// path canonicalizes no identity first, so nothing refuses it before the
	// transaction does, and the transaction failure is reported as a server
	// fault rather than as the caller's own cancellation.
	t.Run("CallerAlreadyGone", func(t *testing.T) {
		ctx := context.Background()
		pg := testkit.Postgres(t)
		repo := newTestRepository(t)

		fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
		before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
		eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)

		goneCtx, cancelGone := context.WithCancel(ctx)
		cancelGone()

		const gone = "UpdateWorkflowBuildStatus (caller already gone)"
		assertBuildStatusCode(
			t, gone,
			repo.UpdateWorkflowBuildStatus(goneCtx, fixture.WorkflowID, fixture.UserID, started, fixture.Generation, "", ""),
			codes.Internal,
		)
		assertBuildStateUndone(ctx, t, pg, fixture, before, eventsBefore, "caller already gone")
	})

	// A trigger on the workflows table refuses the statement itself, which is the
	// failure a broken trigger or an extension produces and the one cause of this
	// arm that is not an interruption: the statement ran to completion and was
	// refused. Nothing has to block for it.
	t.Run("RecordingTheResultRefusedByTheDatabase", func(t *testing.T) {
		ctx := context.Background()
		pg := testkit.Postgres(t)
		repo := newTestRepository(t)

		fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
		before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
		eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)

		faultTrigger(ctx, t, pg, "UPDATE", false)

		err := repo.UpdateWorkflowBuildStatus(
			ctx, fixture.WorkflowID, fixture.UserID, started, fixture.Generation, "", "",
		)
		assertBuildStatusCode(t, "UpdateWorkflowBuildStatus (result refused by a trigger)", err, codes.Internal)
		if message := status.Convert(err).Message(); !strings.Contains(message, "injected statement failure") {
			t.Errorf("trigger-refused message = %q, want it to name the failure that refused the statement", message)
		}
		assertNotRefusedAsMissing(t, "UpdateWorkflowBuildStatus (result refused by a trigger)", err, missingWorkflow, "workflow generation mismatch")
		assertBuildStateUndone(ctx, t, pg, fixture, before, eventsBefore, "result refused by a trigger")
	})

	// The build result is the last write the command makes, so a commit that
	// refuses it is the case where the result is already recorded and only the
	// rollback can take it back. Every statement has succeeded by then, so this
	// is the one failure that leaves the workflow's own build state as the only
	// evidence that the command wrote anything at all.
	t.Run("CommitRefused", func(t *testing.T) {
		ctx := context.Background()
		pg := testkit.Postgres(t)
		repo := newTestRepository(t)

		fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
		before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
		eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)

		faultTrigger(ctx, t, pg, "UPDATE", true)

		err := repo.UpdateWorkflowBuildStatus(
			ctx, fixture.WorkflowID, fixture.UserID, started, fixture.Generation, "", "",
		)
		// The command hands the commit error back as the driver raised it, so it
		// carries no gRPC code of its own and the transport reports it as
		// Unknown. The rollback, not the code, is what this case is about.
		assertBuildStatusCode(t, "UpdateWorkflowBuildStatus (commit refused)", err, codes.Unknown)
		if message := status.Convert(err).Message(); !strings.Contains(message, "injected statement failure") {
			t.Errorf("commit-refused message = %q, want it to name the failure that refused the commit", message)
		}
		assertBuildStateUndone(ctx, t, pg, fixture, before, eventsBefore, "commit refused")
	})
}

// assertBuildStateUndone proves a refused build result recorded none of itself:
// the workflow keeps the build status, generation and resolved image identity it
// had, and nothing was published.
func assertBuildStateUndone(
	ctx context.Context,
	t *testing.T,
	pg *postgres.Postgres,
	fixture *workflowFixture,
	before *workflowMutationState,
	eventsBefore int,
	name string,
) {
	t.Helper()

	assertMutationState(t, name, readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID), before)
	if count := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID); count != eventsBefore {
		t.Errorf("outbox events after %s = %d, want unchanged %d", name, count, eventsBefore)
	}
}

// TestIntegrationUpdateWorkflowBuildStatusAppliesEachAllowedTransition proves
// every transition the build state machine permits is actually taken, with the
// image identity each one is allowed to carry. A permitted transition that was
// silently refused would leave the workflow in a status no later result could
// move it out of, so the assertion is on the durable row rather than on the
// returned error alone.
//
// The generation is delivered unchanged by every step, so a transition that
// moved it would mean the command updated a workflow whose build it had not
// verified.
func TestIntegrationUpdateWorkflowBuildStatusAppliesEachAllowedTransition(t *testing.T) {
	var (
		container = workflowsmodel.KindContainer.ToString()
		heartbeat = workflowsmodel.KindHeartbeat.ToString()
		queued    = workflowsmodel.WorkflowBuildStatusQueued.ToString()
		started   = workflowsmodel.WorkflowBuildStatusStarted.ToString()
		completed = workflowsmodel.WorkflowBuildStatusCompleted.ToString()
		failed    = workflowsmodel.WorkflowBuildStatusFailed.ToString()
		canceled  = workflowsmodel.WorkflowBuildStatusCanceled.ToString()
	)

	cases := []struct {
		name    string
		kind    string
		payload string
		steps   []buildStatusStep
	}{
		{
			// A container build starts, then completes with the image it resolved.
			// The image is recorded with the completing result, never before.
			name:    "ContainerCompletes",
			kind:    container,
			payload: fixturePayload,
			steps: []buildStatusStep{
				{status: started},
				{status: completed, ref: fixtureImage, digest: fixtureImageDigest},
			},
		},
		{
			// A build that fails and one that is canceled both leave the queued
			// state without an image, which is the only identity a non-completed
			// build may carry.
			name:    "ContainerFails",
			kind:    container,
			payload: fixturePayload,
			steps:   []buildStatusStep{{status: failed}},
		},
		{
			name:    "ContainerIsCanceled",
			kind:    container,
			payload: fixturePayload,
			steps:   []buildStatusStep{{status: canceled}},
		},
		{
			// A started build that fails still carries no image, so the image
			// columns are cleared rather than left behind by an earlier step.
			name:    "StartedContainerFails",
			kind:    container,
			payload: fixturePayload,
			steps:   []buildStatusStep{{status: started}, {status: failed}},
		},
		{
			// A heartbeat build has no image at all and completes straight from
			// the queued state, so it never takes the started state a container
			// build does.
			name:    "HeartbeatCompletes",
			kind:    heartbeat,
			payload: heartbeatFixturePayload,
			steps:   []buildStatusStep{{status: completed}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A case with no steps would assert nothing at all and still pass, so
			// the table is checked for that before the fixture is seeded.
			if len(tc.steps) == 0 {
				t.Fatalf("%s declares no build result to apply", tc.name)
			}

			ctx := context.Background()
			pg := testkit.Postgres(t)
			repo := newTestRepository(t)

			fixture := seedWorkflowFixtureOfKind(ctx, t, pg, repo, tc.kind, tc.payload)
			if seeded := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID); seeded.BuildStatus != queued {
				t.Fatalf("seeded build_status = %q, want %q", seeded.BuildStatus, queued)
			}

			for _, step := range tc.steps {
				if err := repo.UpdateWorkflowBuildStatus(
					ctx, fixture.WorkflowID, fixture.UserID, step.status, fixture.Generation, step.ref, step.digest,
				); err != nil {
					t.Fatalf("UpdateWorkflowBuildStatus(%s): %v", step.status, err)
				}
				// Each intermediate result is read back rather than assumed, so a
				// step that was accepted but not recorded cannot pass.
				got := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
				if got.BuildStatus != step.status {
					t.Errorf("build_status after %s = %q, want %q", step.status, got.BuildStatus, step.status)
				}
				if got.Generation != fixture.Generation {
					t.Errorf(
						"generation after %s = %d, want unchanged %d",
						step.status, got.Generation, fixture.Generation,
					)
				}
				if got.ResolvedImageRef.String != step.ref || got.ResolvedImageDigest.String != step.digest {
					t.Errorf(
						"resolved image after %s = (%q, %q), want (%q, %q)",
						step.status, got.ResolvedImageRef.String, got.ResolvedImageDigest.String, step.ref, step.digest,
					)
				}
			}
		})
	}
}

// TestIntegrationUpdateWorkflowBuildStatusRefusesWhatItCannotApply proves the
// refusals a build result can return before it writes anything: a workflow it
// cannot find or does not own, a generation that has moved on, a status its
// current state does not allow, and an image identity that contradicts the kind
// it is completing. Each is the caller's own doing, so each must be reported as
// such and none may move the workflow.
func TestIntegrationUpdateWorkflowBuildStatusRefusesWhatItCannotApply(t *testing.T) {
	const (
		heartbeat = "HEARTBEAT"
		started   = "STARTED"
		completed = "COMPLETED"
		failed    = "FAILED"
	)

	// The build state read matches on both the workflow and its owner, so a
	// result for a workflow the caller does not own is a missing workflow rather
	// than a forbidden one: the command must not confirm that the id it was
	// handed exists at all.
	t.Run("WorkflowItCannotFindOrOwn", func(t *testing.T) {
		ctx := context.Background()
		pg := testkit.Postgres(t)
		repo := newTestRepository(t)

		owner := seedWorkflowFixture(ctx, t, pg, repo, 3)
		stranger := seedWorkflowFixture(ctx, t, pg, repo, 3)
		before := readWorkflowMutationState(ctx, t, pg, owner.WorkflowID)
		eventsBefore := countWorkflowEvents(ctx, t, pg, owner.WorkflowID)

		for _, tc := range []struct {
			name       string
			workflowID string
			userID     string
		}{
			{name: "UnknownWorkflow", workflowID: uuid.NewString(), userID: owner.UserID},
			{name: "WorkflowOwnedByAnotherUser", workflowID: owner.WorkflowID, userID: stranger.UserID},
		} {
			t.Run(tc.name, func(t *testing.T) {
				err := repo.UpdateWorkflowBuildStatus(ctx, tc.workflowID, tc.userID, started, owner.Generation, "", "")
				assertBuildStatusCode(t, tc.name, err, codes.NotFound)
				if message := status.Convert(err).Message(); message != "workflow not found" {
					t.Errorf("%s message = %q, want the missing-workflow refusal", tc.name, message)
				}
			})
		}

		assertMutationState(t, "refused build results", readWorkflowMutationState(ctx, t, pg, owner.WorkflowID), before)
		if count := countWorkflowEvents(ctx, t, pg, owner.WorkflowID); count != eventsBefore {
			t.Errorf("outbox events after the refused build results = %d, want unchanged %d", count, eventsBefore)
		}
	})

	// A generation that has moved on is a result for a build this workflow is no
	// longer running, so applying it would overwrite the state of the build that
	// replaced it.
	t.Run("GenerationThatHasMovedOn", func(t *testing.T) {
		ctx := context.Background()
		pg := testkit.Postgres(t)
		repo := newTestRepository(t)

		fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
		before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)

		superseded := fixture.Generation + 1
		err := repo.UpdateWorkflowBuildStatus(ctx, fixture.WorkflowID, fixture.UserID, started, superseded, "", "")
		assertBuildStatusCode(t, "UpdateWorkflowBuildStatus (superseded generation)", err, codes.FailedPrecondition)
		if message := status.Convert(err).Message(); message != "workflow generation mismatch" {
			t.Errorf("superseded generation message = %q, want the generation-mismatch refusal", message)
		}
		assertMutationState(t, "superseded generation", readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID), before)
	})

	// The state machine only moves a build forward, and each kind through its own
	// states. A result that would walk it backwards, jump a state the kind never
	// takes, or undo a completed build is refused with the transition it refused
	// named, because that is the input a redelivered or out-of-order result looks
	// like.
	t.Run("TransitionTheStateMachineRefuses", func(t *testing.T) {
		ctx := context.Background()
		pg := testkit.Postgres(t)
		repo := newTestRepository(t)

		// A container build never completes straight from the queued state; it
		// has to start first. Completing one is the jump the state machine
		// refuses, and it is the transition that would record an image for a
		// build that never ran.
		containerFixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
		containerBefore := readWorkflowMutationState(ctx, t, pg, containerFixture.WorkflowID)
		err := repo.UpdateWorkflowBuildStatus(
			ctx, containerFixture.WorkflowID, containerFixture.UserID,
			completed, containerFixture.Generation, fixtureImage, fixtureImageDigest,
		)
		assertBuildStatusCode(t, "UpdateWorkflowBuildStatus (queued straight to completed)", err, codes.FailedPrecondition)
		if want := "invalid workflow build transition QUEUED -> COMPLETED"; status.Convert(err).Message() != want {
			t.Errorf("refused transition message = %q, want %q", status.Convert(err).Message(), want)
		}
		assertMutationState(t, "refused transition", readWorkflowMutationState(ctx, t, pg, containerFixture.WorkflowID), containerBefore)

		// A heartbeat build never starts, so a started result for one is refused
		// from a workflow that has already taken the only transition it allows.
		beat := seedWorkflowFixtureOfKind(ctx, t, pg, repo, heartbeat, heartbeatFixturePayload)
		if completeErr := repo.UpdateWorkflowBuildStatus(
			ctx, beat.WorkflowID, beat.UserID, completed, beat.Generation, "", "",
		); completeErr != nil {
			t.Fatalf("UpdateWorkflowBuildStatus (heartbeat completed): %v", completeErr)
		}
		err = repo.UpdateWorkflowBuildStatus(ctx, beat.WorkflowID, beat.UserID, started, beat.Generation, "", "")
		assertBuildStatusCode(t, "UpdateWorkflowBuildStatus (heartbeat started after completed)", err, codes.FailedPrecondition)
		if want := "invalid workflow build transition COMPLETED -> STARTED"; status.Convert(err).Message() != want {
			t.Errorf("refused heartbeat transition message = %q, want %q", status.Convert(err).Message(), want)
		}

		// A completed container build that reports failure for the same
		// generation would undo a result a later run depends on.
		built := seedWorkflowFixture(ctx, t, pg, repo, 3)
		completeFixtureBuild(ctx, t, repo, built)
		settled := readWorkflowMutationState(ctx, t, pg, built.WorkflowID)

		err = repo.UpdateWorkflowBuildStatus(ctx, built.WorkflowID, built.UserID, failed, built.Generation, "", "")
		assertBuildStatusCode(t, "UpdateWorkflowBuildStatus (completed to failed)", err, codes.FailedPrecondition)
		assertMutationState(t, "refused completed-to-failed", readWorkflowMutationState(ctx, t, pg, built.WorkflowID), settled)
	})

	// The image identity a build result carries has to match the kind and the
	// status it completes. These refusals are the ones that keep a heartbeat
	// build from recording an image it never resolves, and a container build
	// from completing without the identity every later run executes.
	t.Run("ImageIdentityTheKindRefuses", func(t *testing.T) {
		ctx := context.Background()
		pg := testkit.Postgres(t)
		repo := newTestRepository(t)

		// An image identity on a build that has not completed is refused for any
		// kind, so the recorded image always means a completed build resolved it.
		containerFixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
		heartbeatFixture := seedWorkflowFixtureOfKind(ctx, t, pg, repo, heartbeat, heartbeatFixturePayload)
		states := map[string]*workflowMutationState{
			containerFixture.WorkflowID: readWorkflowMutationState(ctx, t, pg, containerFixture.WorkflowID),
			heartbeatFixture.WorkflowID: readWorkflowMutationState(ctx, t, pg, heartbeatFixture.WorkflowID),
		}

		for _, tc := range []struct {
			name    string
			fixture *workflowFixture
		}{
			{name: "ImageOnANonCompletedContainerBuild", fixture: containerFixture},
			{name: "ImageOnANonCompletedHeartbeatBuild", fixture: heartbeatFixture},
		} {
			t.Run(tc.name, func(t *testing.T) {
				err := repo.UpdateWorkflowBuildStatus(
					ctx, tc.fixture.WorkflowID, tc.fixture.UserID, started,
					tc.fixture.Generation, fixtureImage, fixtureImageDigest,
				)
				assertBuildStatusCode(t, tc.name, err, codes.InvalidArgument)
				if want := "resolved image identity is only valid for completed builds"; status.Convert(err).Message() != want {
					t.Errorf("%s message = %q, want %q", tc.name, status.Convert(err).Message(), want)
				}
			})
		}

		// A completed container build is the one result that must carry the
		// image. Completing without it would leave a run with no image to
		// execute, so the result is refused and the started state kept.
		startedContainer := seedWorkflowFixture(ctx, t, pg, repo, 3)
		if startErr := repo.UpdateWorkflowBuildStatus(
			ctx, startedContainer.WorkflowID, startedContainer.UserID, started, startedContainer.Generation, "", "",
		); startErr != nil {
			t.Fatalf("UpdateWorkflowBuildStatus(started): %v", startErr)
		}
		startedState := readWorkflowMutationState(ctx, t, pg, startedContainer.WorkflowID)
		err := repo.UpdateWorkflowBuildStatus(
			ctx, startedContainer.WorkflowID, startedContainer.UserID, completed, startedContainer.Generation, "", "",
		)
		assertBuildStatusCode(t, "completed container build without an image", err, codes.InvalidArgument)
		if want := "completed container builds require image reference and digest"; status.Convert(err).Message() != want {
			t.Errorf("completed container build without an image message = %q, want %q", status.Convert(err).Message(), want)
		}
		assertMutationState(
			t, "refused image-less completion",
			readWorkflowMutationState(ctx, t, pg, startedContainer.WorkflowID),
			startedState,
		)

		// A completed heartbeat build is the one result that must not carry one,
		// because a heartbeat never resolves an image at all.
		queuedHeartbeat := seedWorkflowFixtureOfKind(ctx, t, pg, repo, heartbeat, heartbeatFixturePayload)
		queuedState := readWorkflowMutationState(ctx, t, pg, queuedHeartbeat.WorkflowID)
		err = repo.UpdateWorkflowBuildStatus(
			ctx, queuedHeartbeat.WorkflowID, queuedHeartbeat.UserID, completed,
			queuedHeartbeat.Generation, fixtureImage, fixtureImageDigest,
		)
		assertBuildStatusCode(t, "completed heartbeat build carrying an image", err, codes.InvalidArgument)
		if want := "heartbeat builds must not contain resolved image identity"; status.Convert(err).Message() != want {
			t.Errorf("completed heartbeat build carrying an image message = %q, want %q", status.Convert(err).Message(), want)
		}
		assertMutationState(
			t, "refused image-bearing heartbeat completion",
			readWorkflowMutationState(ctx, t, pg, queuedHeartbeat.WorkflowID),
			queuedState,
		)

		for _, fixture := range []*workflowFixture{containerFixture, heartbeatFixture} {
			assertMutationState(
				t, "refused image identities",
				readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID),
				states[fixture.WorkflowID],
			)
		}
	})
}
