//nolint:testpackage // Exercises worker lifecycle contracts without exporting helpers.
package workflow

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
)

type lifecycleLock struct {
	acquired bool
	err      error
	keys     []string
	released []string
}

func (s *lifecycleLock) AcquireDistributedLock(_ context.Context, key string, ttl time.Duration) (bool, error) {
	if ttl != 5*time.Minute {
		return false, errors.New("unexpected lock expiry")
	}
	s.keys = append(s.keys, key)
	return s.acquired, s.err
}

func (s *lifecycleLock) ReleaseDistributedLock(_ context.Context, key string) error {
	s.released = append(s.released, key)
	return nil
}

func lifecycleWorkflow() *workflowspb.GetWorkflowByIDResponse {
	return &workflowspb.GetWorkflowByIDResponse{
		Id: "workflow-1", UserId: "user-1", Name: "workflow", Generation: 3,
		Kind:        workflowsmodel.KindHeartbeat.ToString(),
		BuildStatus: workflowsmodel.WorkflowBuildStatusCompleted.ToString(), Interval: 5,
	}
}

func lifecycleEvent(action workflowsmodel.Action) *workflowsmodel.WorkflowEvent {
	return &workflowsmodel.WorkflowEvent{ID: "workflow-1", UserID: "user-1", Generation: 3, Action: action}
}

func lifecycleRepository(workflow *workflowspb.GetWorkflowByIDResponse) (*Repository, *lifecycleLock) {
	lock := &lifecycleLock{acquired: true}
	return &Repository{auth: testAuth{}, rdb: lock, svc: &Services{Workflows: &testWorkflowsClient{workflow: workflow}, Jobs: &testJobsClient{}, Notifications: testNotificationsClient{}}}, lock
}

func TestRescheduleWorkflowGuardsAndFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		configure    func(*Repository, *lifecycleLock, *workflowspb.GetWorkflowByIDResponse, *workflowsmodel.WorkflowEvent)
		scheduleErr  error
		want         codes.Code
		wantLock     bool
		wantSchedule bool
	}{
		{name: "authorization", configure: func(r *Repository, _ *lifecycleLock, _ *workflowspb.GetWorkflowByIDResponse, _ *workflowsmodel.WorkflowEvent) {
			r.auth = testAuth{issueToken: func(context.Context, string) (string, error) { return "", status.Error(codes.Unavailable, "auth") }}
		}, want: codes.Unavailable},
		{name: "missing workflow", configure: func(r *Repository, _ *lifecycleLock, _ *workflowspb.GetWorkflowByIDResponse, _ *workflowsmodel.WorkflowEvent) {
			r.svc.Workflows = &testWorkflowsClient{}
		}, want: codes.NotFound},
		{name: "stale generation", configure: func(_ *Repository, _ *lifecycleLock, _ *workflowspb.GetWorkflowByIDResponse, e *workflowsmodel.WorkflowEvent) {
			e.Generation = 2
		}},
		{name: "terminated", configure: func(_ *Repository, _ *lifecycleLock, w *workflowspb.GetWorkflowByIDResponse, _ *workflowsmodel.WorkflowEvent) {
			w.TerminatedAt = time.Now().Format(time.RFC3339Nano)
		}, want: codes.FailedPrecondition},
		{name: "unbuilt", configure: func(_ *Repository, _ *lifecycleLock, w *workflowspb.GetWorkflowByIDResponse, _ *workflowsmodel.WorkflowEvent) {
			w.BuildStatus = workflowsmodel.WorkflowBuildStatusStarted.ToString()
		}},
		{name: "busy lock", configure: func(_ *Repository, l *lifecycleLock, _ *workflowspb.GetWorkflowByIDResponse, _ *workflowsmodel.WorkflowEvent) {
			l.acquired = false
		}, want: codes.Aborted, wantLock: true},
		{name: "lock unavailable", configure: func(_ *Repository, l *lifecycleLock, _ *workflowspb.GetWorkflowByIDResponse, _ *workflowsmodel.WorkflowEvent) {
			l.err = errors.New("redis unavailable")
		}, want: codes.Aborted, wantLock: true},
		{name: "schedule unavailable", scheduleErr: status.Error(codes.Unavailable, "jobs"), want: codes.Unavailable, wantLock: true, wantSchedule: true},
		{name: "schedule precondition race", scheduleErr: status.Error(codes.FailedPrecondition, "generation changed"), want: codes.OK, wantLock: true, wantSchedule: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := lifecycleWorkflow()
			r, l := lifecycleRepository(w)
			e := lifecycleEvent(workflowsmodel.ActionReschedule)
			called := false
			r.svc.Jobs = &testJobsClient{scheduleJob: func(context.Context, *jobspb.ScheduleJobRequest) (*jobspb.ScheduleJobResponse, error) {
				called = true
				return nil, tt.scheduleErr
			}}
			if tt.configure != nil {
				tt.configure(r, l, w, e)
			}
			if got := status.Code(r.rescheduleWorkflow(t.Context(), e)); got != tt.want {
				t.Fatalf("code=%s want %s", got, tt.want)
			}
			if called != tt.wantSchedule || (len(l.keys) > 0) != tt.wantLock {
				t.Fatalf("schedule=%v locks=%v", called, l.keys)
			}
			if tt.wantSchedule && (len(l.released) != 1 || l.released[0] != l.keys[0]) {
				t.Fatal("acquired lock not released")
			}
		})
	}
}

func TestRescheduleWorkflowReplayPreservesCommandIdentity(t *testing.T) {
	t.Parallel()
	w := lifecycleWorkflow()
	r, l := lifecycleRepository(w)
	e := lifecycleEvent(workflowsmodel.ActionReschedule)
	var requests []*jobspb.ScheduleJobRequest
	r.svc.Jobs = &testJobsClient{scheduleJob: func(_ context.Context, req *jobspb.ScheduleJobRequest) (*jobspb.ScheduleJobResponse, error) {
		requests = append(requests, req)
		return &jobspb.ScheduleJobResponse{Id: "job-1"}, nil
	}}
	start := time.Now().Add(5 * time.Minute)
	for range 2 {
		if err := r.rescheduleWorkflow(t.Context(), e); err != nil {
			t.Fatal(err)
		}
	}
	for _, req := range requests {
		at, err := time.Parse(time.RFC3339Nano, req.GetScheduledAt())
		if err != nil || at.Before(start) || at.After(time.Now().Add(5*time.Minute)) {
			t.Fatalf("scheduled time=%s", req.GetScheduledAt())
		}
		if req.GetWorkflowId() != w.GetId() || req.GetUserId() != w.GetUserId() || req.GetWorkflowGeneration() != 3 ||
			req.GetTrigger() != jobsmodel.JobTriggerAutomatic.ToString() ||
			req.GetIdempotencyKey() != idempotency.AutomaticScheduleEventKey(workflowOccurrenceKey(e)) {
			t.Fatalf("schedule request=%v", req)
		}
	}
	if len(requests) != 2 || len(l.released) != 2 || l.keys[0] != l.keys[1] {
		t.Fatalf("requests=%d lock=%+v", len(requests), l)
	}
}

func TestMarkRunningJobsCanceledPaginationAndTimestamps(t *testing.T) {
	t.Parallel()
	w := lifecycleWorkflow()
	w.Kind = workflowsmodel.KindContainer.ToString()
	r, _ := lifecycleRepository(w)
	var canceled, cursors []string
	r.svc.Jobs = &testJobsClient{listJobs: func(_ context.Context, req *jobspb.ListJobsRequest) (*jobspb.ListJobsResponse, error) {
		if req.GetWorkflowId() != w.GetId() || req.GetUserId() != w.GetUserId() || req.GetFilters().GetStatus() != jobsmodel.JobStatusRunning.ToString() {
			t.Fatalf("list=%v", req)
		}
		cursors = append(cursors, req.GetCursor())
		if req.GetCursor() == "" {
			return &jobspb.ListJobsResponse{
				Jobs:   []*jobspb.JobsResponse{{Id: "job-1"}, {Id: "invalid", StartedAt: "invalid"}, {Id: "job-2", StartedAt: time.Now().Format(time.RFC3339Nano)}},
				Cursor: "page-2",
			}, nil
		}
		return &jobspb.ListJobsResponse{Jobs: []*jobspb.JobsResponse{{Id: "job-3"}}}, nil
	}, cancelJob: func(_ context.Context, req *jobspb.CancelJobRequest) error {
		canceled = append(canceled, req.GetId())
		if req.GetCommandId() != idempotency.JobCancelCommandID(req.GetId()) {
			t.Fatal("unstable cancellation identity")
		}
		return nil
	}}
	jobs, err := r.markRunningJobsCanceled(t.Context(), w, w.GetUserId())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(canceled, []string{"job-1", "job-2", "job-3"}) || !reflect.DeepEqual(cursors, []string{"", "page-2"}) || len(jobs) != 3 {
		t.Fatalf("canceled=%v cursors=%v cleanup=%v", canceled, cursors, jobs)
	}
}

func TestMarkRunningJobsCanceledFailures(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"authorization", "list", "cancel", "empty"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			w := lifecycleWorkflow()
			r, _ := lifecycleRepository(w)
			if stage == "authorization" {
				r.auth = testAuth{issueToken: func(context.Context, string) (string, error) { return "", status.Error(codes.Unavailable, "auth") }}
			}
			r.svc.Jobs = &testJobsClient{listJobs: func(context.Context, *jobspb.ListJobsRequest) (*jobspb.ListJobsResponse, error) {
				if stage == "list" {
					return nil, status.Error(codes.Unavailable, "list")
				}
				if stage == "empty" {
					return &jobspb.ListJobsResponse{Cursor: "unused"}, nil
				}
				return &jobspb.ListJobsResponse{Jobs: []*jobspb.JobsResponse{{Id: "job-1"}}}, nil
			}, cancelJob: func(context.Context, *jobspb.CancelJobRequest) error {
				return status.Error(codes.Unavailable, "cancel")
			}}
			jobs, err := r.markRunningJobsCanceled(t.Context(), w, w.GetUserId())
			want := codes.Unavailable
			if stage == "empty" {
				want = codes.OK
			}
			if status.Code(err) != want || len(jobs) != 0 {
				t.Fatalf("jobs=%v err=%v", jobs, err)
			}
		})
	}
}

type lifecycleJobsClient struct {
	*testJobsClient
	cancel func(context.Context, *jobspb.CancelJobRequest) (*jobspb.CancelJobResponse, error)
}

func (c *lifecycleJobsClient) CancelJob(ctx context.Context, req *jobspb.CancelJobRequest, _ ...grpc.CallOption) (*jobspb.CancelJobResponse, error) {
	return c.cancel(ctx, req)
}

func TestTerminateWorkflowGuards(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"authorization", "lookup", "stale", "busy", "lock error"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			w := lifecycleWorkflow()
			r, l := lifecycleRepository(w)
			e := lifecycleEvent(workflowsmodel.ActionTerminate)
			want := codes.Aborted
			jobsCalled := false
			r.svc.Jobs = &testJobsClient{listJobs: func(context.Context, *jobspb.ListJobsRequest) (*jobspb.ListJobsResponse, error) {
				jobsCalled = true
				return &jobspb.ListJobsResponse{}, nil
			}, cancelJob: func(context.Context, *jobspb.CancelJobRequest) error { jobsCalled = true; return nil }}
			switch stage {
			case "authorization":
				r.auth = testAuth{issueToken: func(context.Context, string) (string, error) { return "", status.Error(codes.Unavailable, "auth") }}
				want = codes.Unavailable
			case "lookup":
				r.svc.Workflows = &testWorkflowsClient{}
				want = codes.NotFound
			case "stale":
				e.Generation = 2
				want = codes.OK
			case "busy":
				l.acquired = false
			case "lock error":
				l.err = errors.New("redis unavailable")
			}
			if got := status.Code(r.terminateWorkflow(t.Context(), e)); got != want {
				t.Fatalf("code=%s want=%s", got, want)
			}
			if len(l.released) != 0 || jobsCalled {
				t.Fatalf("unexpected effects: releases=%v jobs=%v", l.released, jobsCalled)
			}
			if stage != "busy" && stage != "lock error" && len(l.keys) != 0 {
				t.Fatalf("guard acquired lock: %v", l.keys)
			}
		})
	}
}

func TestTerminateWorkflowCancelsAllStatusesBeforeCleanupAndReplays(t *testing.T) {
	t.Parallel()
	w := lifecycleWorkflow()
	w.Kind = workflowsmodel.KindContainer.ToString()
	w.TerminatedAt = time.Now().Format(time.RFC3339Nano)
	r, l := lifecycleRepository(w)
	events := &orderedEvents{}
	notifications := make(chan string, 2)
	r.svc.Notifications = testNotificationsClient{createNotification: func(_ context.Context, req *notificationspb.CreateNotificationRequest) (*notificationspb.CreateNotificationResponse, error) {
		if notificationTitle(req.GetPayload()) == "Workflow Terminated" {
			notifications <- req.GetIdempotencyKey()
		}
		return &notificationspb.CreateNotificationResponse{Id: "notification"}, nil
	}}
	canceled := make(map[string]bool)
	r.svc.Jobs = &lifecycleJobsClient{testJobsClient: &testJobsClient{listJobs: func(_ context.Context, req *jobspb.ListJobsRequest) (*jobspb.ListJobsResponse, error) {
		state := req.GetFilters().GetStatus()
		events.add("list-" + state)
		if state == jobsmodel.JobStatusCanceled.ToString() {
			return &jobspb.ListJobsResponse{Jobs: []*jobspb.JobsResponse{{Id: "retry", ContainerId: "container-retry", RuntimeEndpoint: "runtime"}}}, nil
		}
		if canceled[state] {
			return &jobspb.ListJobsResponse{}, nil
		}
		return &jobspb.ListJobsResponse{Jobs: []*jobspb.JobsResponse{{Id: "job-" + state, Status: state}}}, nil
	}}, cancel: func(_ context.Context, req *jobspb.CancelJobRequest) (*jobspb.CancelJobResponse, error) {
		state := strings.TrimPrefix(req.GetId(), "job-")
		if canceled[state] {
			t.Fatal("already canceled job canceled again")
		}
		canceled[state] = true
		events.add("cancel-" + req.GetId())
		if req.GetTerminalReasonCode() != "WORKFLOW_TERMINATED" || req.GetCommandId() != idempotency.JobCancelCommandID(req.GetId()) {
			t.Fatalf("cancel=%v", req)
		}
		return &jobspb.CancelJobResponse{Id: req.GetId(), PreviousStatus: state, ContainerId: "container-" + state, RuntimeEndpoint: "runtime"}, nil
	}}
	r.svc.CsvcForEndpoint = func(string, string) (ContainerSvc, error) { return &testContainerSvc{events: events}, nil }
	e := lifecycleEvent(workflowsmodel.ActionTerminate)
	for range 2 {
		if err := r.terminateWorkflow(t.Context(), e); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		"list-RUNNING", "cancel-job-RUNNING", "list-PENDING", "cancel-job-PENDING", "list-QUEUED", "cancel-job-QUEUED",
		"terminate", "remove", "terminate", "remove", "terminate", "remove", "list-CANCELED", "terminate", "remove",
		"list-RUNNING", "list-PENDING", "list-QUEUED", "list-CANCELED", "terminate", "remove",
	}
	assertEvents(t, events.items(), want)
	if !reflect.DeepEqual(l.released, l.keys) || len(l.released) != 2 {
		t.Fatalf("release=%v", l.released)
	}
	assertTerminationNotificationReplay(t, notifications, idempotency.WorkflowNotificationEventKey(w.GetId(), "Workflow Terminated", workflowOccurrenceKey(e)))
}

// assertTerminationNotificationReplay waits for the detached termination
// notifications of both terminations. Each wait is bounded because delivery runs
// on a detached goroutine after terminateWorkflow has already returned.
func assertTerminationNotificationReplay(t *testing.T, notifications <-chan string, want string) {
	t.Helper()
	var keys []string
	for range 2 {
		select {
		case key := <-notifications:
			keys = append(keys, key)
		case <-time.After(5 * time.Second):
			t.Fatal("termination notification missing")
		}
	}
	if keys[0] == "" || keys[0] != keys[1] {
		t.Fatalf("notification replay identity=%v", keys)
	}
	if keys[0] != want {
		t.Fatalf("termination notification key=%q want=%q", keys[0], want)
	}
}

func TestTerminateWorkflowStopsAtFailureAndReleasesLock(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"RUNNING", "PENDING", "QUEUED", "cleanup", "CANCELED"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			w := lifecycleWorkflow()
			w.Kind = workflowsmodel.KindContainer.ToString()
			r, l := lifecycleRepository(w)
			var listed []string
			cleanupCalls := 0
			r.svc.Jobs = &testJobsClient{listJobs: func(_ context.Context, req *jobspb.ListJobsRequest) (*jobspb.ListJobsResponse, error) {
				state := req.GetFilters().GetStatus()
				listed = append(listed, state)
				if state == stage {
					return nil, status.Error(codes.Unavailable, "list failed")
				}
				if stage == "cleanup" && state == "RUNNING" {
					return &jobspb.ListJobsResponse{Jobs: []*jobspb.JobsResponse{{Id: "job-1"}}}, nil
				}
				return &jobspb.ListJobsResponse{}, nil
			}}
			r.svc.CsvcForEndpoint = func(string, string) (ContainerSvc, error) {
				cleanupCalls++
				return nil, status.Error(codes.Unavailable, "runtime unavailable")
			}
			if status.Code(r.terminateWorkflow(t.Context(), lifecycleEvent(workflowsmodel.ActionTerminate))) != codes.Unavailable {
				t.Fatal("expected failure")
			}
			if len(l.released) != 1 || l.released[0] != l.keys[0] {
				t.Fatal("lock not released on error")
			}
			expected := map[string]int{"RUNNING": 1, "PENDING": 2, "QUEUED": 3, "cleanup": 3, "CANCELED": 4}[stage]
			if len(listed) != expected {
				t.Fatalf("listed=%v", listed)
			}
			if (cleanupCalls > 0) != (stage == "cleanup") {
				t.Fatalf("cleanup calls=%d", cleanupCalls)
			}
		})
	}
}

func TestCleanupJobsWithStatusContinuesAfterErrorAcrossPages(t *testing.T) {
	t.Parallel()
	w := lifecycleWorkflow()
	w.Kind = workflowsmodel.KindContainer.ToString()
	r, _ := lifecycleRepository(w)
	first := status.Error(codes.Unavailable, "first runtime failed")
	var cursors, endpoints []string
	r.svc.Jobs = &testJobsClient{listJobs: func(_ context.Context, req *jobspb.ListJobsRequest) (*jobspb.ListJobsResponse, error) {
		if req.GetWorkflowId() != w.GetId() || req.GetUserId() != w.GetUserId() || req.GetFilters().GetStatus() != "CANCELED" {
			t.Fatalf("list=%v", req)
		}
		cursors = append(cursors, req.GetCursor())
		if req.GetCursor() == "" {
			return &jobspb.ListJobsResponse{Jobs: []*jobspb.JobsResponse{{ContainerId: "c1", RuntimeEndpoint: "first"}, {ContainerId: "c2", RuntimeEndpoint: "second"}}, Cursor: "next"}, nil
		}
		return &jobspb.ListJobsResponse{Jobs: []*jobspb.JobsResponse{{ContainerId: "c3", RuntimeEndpoint: "third"}}}, nil
	}}
	r.svc.CsvcForEndpoint = func(_, endpoint string) (ContainerSvc, error) {
		endpoints = append(endpoints, endpoint)
		if endpoint == "first" {
			return nil, first
		}
		if endpoint == "second" {
			return nil, status.Error(codes.Internal, "second failure")
		}
		return &testContainerSvc{}, nil
	}
	if err := r.cleanupJobsWithStatus(t.Context(), w, w.GetUserId(), "CANCELED"); !errors.Is(err, first) {
		t.Fatalf("error=%v", err)
	}
	if !reflect.DeepEqual(cursors, []string{"", "next"}) || !reflect.DeepEqual(endpoints, []string{"first", "second", "third"}) {
		t.Fatalf("cursors=%v endpoints=%v", cursors, endpoints)
	}
}

func TestCleanupJobsWithStatusFailuresAndEmptyResult(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"authorization", "list", "empty"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			w := lifecycleWorkflow()
			r, _ := lifecycleRepository(w)
			if stage == "authorization" {
				r.auth = testAuth{issueToken: func(context.Context, string) (string, error) { return "", status.Error(codes.Unavailable, "auth") }}
			}
			r.svc.Jobs = &testJobsClient{listJobs: func(context.Context, *jobspb.ListJobsRequest) (*jobspb.ListJobsResponse, error) {
				if stage == "list" {
					return nil, status.Error(codes.Unavailable, "jobs")
				}
				return &jobspb.ListJobsResponse{}, nil
			}}
			want := codes.Unavailable
			if stage == "empty" {
				want = codes.OK
			}
			if got := status.Code(r.cleanupJobsWithStatus(t.Context(), w, w.GetUserId(), "CANCELED")); got != want {
				t.Fatalf("code=%s", got)
			}
		})
	}
}
