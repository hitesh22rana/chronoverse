//nolint:testpackage // Tests unexported workflow helpers without widening production API.
package workflow

import (
	"context"
	"slices"
	"sync"
	"testing"

	"google.golang.org/grpc"

	notificationsmodel "github.com/hitesh22rana/chronoverse/internal/model/notifications"
	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

// recordedTerminalEffectCommand is the workflow-side command one JOB_FAILED
// event asked for. JobID is the durable identity the workflows repository keys
// its terminal-effect ledger on, so a redelivery that changed it would double
// count the same failure.
type recordedTerminalEffectCommand struct {
	WorkflowID string
	UserID     string
	JobID      string
}

// recordedNotification is one notification request and the durable key the
// notifications repository dedupes on.
type recordedNotification struct {
	Title string
	Kind  string
	Key   string
}

// recorder collects the durable identities emitted by the handler. It is shared
// between the sequential handler invocations below, so it never crosses
// goroutines; the mutex keeps that guarantee explicit for future callers.
type recorder struct {
	mu            sync.Mutex
	commands      []recordedTerminalEffectCommand
	notifications []recordedNotification
}

func (r *recorder) recordCommand(command recordedTerminalEffectCommand) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, command)
}

func (r *recorder) recordNotification(notification recordedNotification) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notifications = append(r.notifications, notification)
}

func (r *recorder) commandsPerRun() []recordedTerminalEffectCommand {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.commands)
}

func (r *recorder) notificationsPerRun() []recordedNotification {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.notifications)
}

// TestHandleJobFailedReplayReusesDurableIdentities proves a redelivered
// JOB_FAILED event carries the same durable identities on every attempt. The
// failure command keeps the job id the workflows repository keys its terminal
// effect on, and both notifications keep the occurrence-scoped keys that let the
// notifications repository collapse a replay, so neither the failure counter nor
// the user's notification list can grow twice from one delivery.
//
//nolint:gocyclo // One flow pins every durable identity a redelivered failure event may emit.
func TestHandleJobFailedReplayReusesDurableIdentities(t *testing.T) {
	t.Parallel()

	const (
		workflowID        = "11111111-1111-4111-8111-111111111111"
		userID            = "22222222-2222-4222-8222-222222222222"
		jobID             = "33333333-3333-4333-8333-333333333333"
		failedTitle       = "Job Execution Failed"
		terminatedTitle   = "Workflow Terminated"
		replayedThreshold = true
	)
	records := &recorder{}

	repo := &Repository{
		auth: testAuth{},
		svc: &Services{
			Workflows: &testWorkflowsClient{
				workflow: &workflowspb.GetWorkflowByIDResponse{
					Id:                               workflowID,
					UserId:                           userID,
					Name:                             "nightly-backup",
					MaxConsecutiveJobFailuresAllowed: 3,
				},
				incrementFailures: func(_ context.Context, req *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest) (*workflowspb.IncrementWorkflowConsecutiveJobFailuresCountResponse, error) {
					records.recordCommand(recordedTerminalEffectCommand{
						WorkflowID: req.GetId(),
						UserID:     req.GetUserId(),
						JobID:      req.GetJobId(),
					})
					// The repository replays its recorded outcome for a redelivered
					// job, so the handler observes the same threshold on every run.
					return &workflowspb.IncrementWorkflowConsecutiveJobFailuresCountResponse{ThresholdReached: replayedThreshold}, nil
				},
			},
			Notifications: testNotificationsClient{
				createNotification: func(_ context.Context, req *notificationspb.CreateNotificationRequest) (*notificationspb.CreateNotificationResponse, error) {
					records.recordNotification(recordedNotification{
						Title: notificationTitle(req.GetPayload()),
						Kind:  req.GetKind(),
						Key:   req.GetIdempotencyKey(),
					})
					return &notificationspb.CreateNotificationResponse{Id: "notification-1"}, nil
				},
			},
		},
	}

	event := &workflowsmodel.WorkflowEvent{
		EventKey: idempotency.JobWorkflowEventKey(jobID, workflowsmodel.ActionJobFailed.ToString()),
		ID:       workflowID,
		UserID:   userID,
		Action:   workflowsmodel.ActionJobFailed,
		JobID:    jobID,
	}

	for run := range 2 {
		if err := repo.handleJobFailed(t.Context(), event); err != nil {
			t.Fatalf("handleJobFailed() run %d error = %v", run+1, err)
		}
	}

	commands := records.commandsPerRun()
	if len(commands) != 2 {
		t.Fatalf("terminal-effect commands = %d, want one per delivery", len(commands))
	}
	if commands[0] != commands[1] {
		t.Fatalf("terminal-effect command identities differ across deliveries: %+v vs %+v", commands[0], commands[1])
	}
	if commands[0].JobID != jobID {
		t.Fatalf("terminal-effect command job id = %q, want the delivered job id %q", commands[0].JobID, jobID)
	}
	if commands[0].WorkflowID != workflowID || commands[0].UserID != userID {
		t.Fatalf("terminal-effect command identity = %q/%q, want %q/%q", commands[0].WorkflowID, commands[0].UserID, workflowID, userID)
	}

	notifications := records.notificationsPerRun()
	if len(notifications) != 4 {
		t.Fatalf("notifications = %d, want a failure and a termination notification per delivery", len(notifications))
	}
	wantKeys := map[string]string{
		failedTitle:     idempotency.JobNotificationEventKey(jobID, failedTitle),
		terminatedTitle: idempotency.WorkflowNotificationEventKey(workflowID, terminatedTitle, jobID),
	}
	wantKinds := map[string]string{
		failedTitle:     notificationsmodel.KindWebError.ToString(),
		terminatedTitle: notificationsmodel.KindWebAlert.ToString(),
	}
	for run := range 2 {
		for _, notification := range notifications[run*2 : run*2+2] {
			wantKey, known := wantKeys[notification.Title]
			if !known {
				t.Fatalf("delivery %d notified %q, want only %v", run+1, notification.Title, wantKeys)
			}
			if notification.Key != wantKey {
				t.Fatalf("delivery %d %q key = %q, want the deterministic %q", run+1, notification.Title, notification.Key, wantKey)
			}
			if notification.Kind != wantKinds[notification.Title] {
				t.Fatalf("delivery %d %q kind = %q, want %q", run+1, notification.Title, notification.Kind, wantKinds[notification.Title])
			}
		}
	}
	if notifications[0] != notifications[2] || notifications[1] != notifications[3] {
		t.Fatalf("notification identities differ across deliveries: %+v vs %+v", notifications[0:2], notifications[2:4])
	}
	if notifications[0].Title != failedTitle || notifications[1].Title != terminatedTitle {
		t.Fatalf("notification titles = %q/%q, want %q/%q", notifications[0].Title, notifications[1].Title, failedTitle, terminatedTitle)
	}
}

// TestHandleJobCompletedReplayReusesDurableIdentities proves the completion
// handler is keyed the same way: redelivering JOB_COMPLETED reuses the reset
// command's job id and the job notification's deterministic key, so the reset
// replays instead of clearing a counter a newer failure raised again.
func TestHandleJobCompletedReplayReusesDurableIdentities(t *testing.T) {
	t.Parallel()

	const (
		workflowID     = "44444444-4444-4444-8444-444444444444"
		userID         = "55555555-5555-4555-8555-555555555555"
		jobID          = "66666666-6666-4666-8666-666666666666"
		completedTitle = "Job Execution Completed"
	)

	records := &recorder{}
	keys := &orderedEvents{}

	repo := &Repository{
		auth: testAuth{},
		svc: &Services{
			Workflows: resetRecordingWorkflowsClient{
				testWorkflowsClient: &testWorkflowsClient{
					workflow: &workflowspb.GetWorkflowByIDResponse{
						Id:     workflowID,
						UserId: userID,
						Name:   "nightly-backup",
					},
				},
				resetCounters: func(_ context.Context, req *workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest) error {
					records.recordCommand(recordedTerminalEffectCommand{
						WorkflowID: req.GetId(),
						UserID:     req.GetUserId(),
						JobID:      req.GetJobId(),
					})
					return nil
				},
			},
			Notifications: testNotificationsClient{
				createNotification: func(_ context.Context, req *notificationspb.CreateNotificationRequest) (*notificationspb.CreateNotificationResponse, error) {
					keys.add(req.GetIdempotencyKey())
					return &notificationspb.CreateNotificationResponse{Id: "notification-1"}, nil
				},
			},
		},
	}

	event := &workflowsmodel.WorkflowEvent{
		EventKey: idempotency.JobWorkflowEventKey(jobID, workflowsmodel.ActionJobCompleted.ToString()),
		ID:       workflowID,
		UserID:   userID,
		Action:   workflowsmodel.ActionJobCompleted,
		JobID:    jobID,
	}

	for run := range 2 {
		if err := repo.handleJobCompleted(t.Context(), event); err != nil {
			t.Fatalf("handleJobCompleted() run %d error = %v", run+1, err)
		}
	}

	resets := records.commandsPerRun()
	if len(resets) != 2 {
		t.Fatalf("reset commands = %d, want one per delivery", len(resets))
	}
	if resets[0] != resets[1] {
		t.Fatalf("reset command identities differ across deliveries: %+v vs %+v", resets[0], resets[1])
	}
	if resets[0].JobID != jobID || resets[0].WorkflowID != workflowID || resets[0].UserID != userID {
		t.Fatalf("reset command identity = %+v, want the delivered job %q on workflow %q", resets[0], jobID, workflowID)
	}

	gotKeys := keys.items()
	if len(gotKeys) != 2 {
		t.Fatalf("notification keys = %v, want one per delivery", gotKeys)
	}
	wantKey := idempotency.JobNotificationEventKey(jobID, completedTitle)
	if gotKeys[0] != wantKey || gotKeys[1] != wantKey {
		t.Fatalf("notification keys = %v, want the deterministic %q on both deliveries", gotKeys, wantKey)
	}
}

// resetRecordingWorkflowsClient captures the counter-reset command the
// completion handler issues. It embeds the shared workflows test client so only
// the reset call differs.
type resetRecordingWorkflowsClient struct {
	*testWorkflowsClient
	resetCounters func(context.Context, *workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest) error
}

func (c resetRecordingWorkflowsClient) ResetWorkflowConsecutiveJobFailuresCount(
	ctx context.Context,
	req *workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest,
	_ ...grpc.CallOption,
) (*workflowspb.ResetWorkflowConsecutiveJobFailuresCountResponse, error) {
	if err := c.resetCounters(ctx, req); err != nil {
		return nil, err
	}
	return &workflowspb.ResetWorkflowConsecutiveJobFailuresCountResponse{}, nil
}
