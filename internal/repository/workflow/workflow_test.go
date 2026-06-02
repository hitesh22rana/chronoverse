//nolint:testpackage // Tests unexported workflow helpers without widening production API.
package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
)

func TestIsStaleWorkflowEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		current       int64
		event         int64
		expectedStale bool
	}{
		{
			name:          "current generation",
			current:       3,
			event:         3,
			expectedStale: false,
		},
		{
			name:          "stale generation",
			current:       3,
			event:         2,
			expectedStale: true,
		},
		{
			name:          "future generation",
			current:       3,
			event:         4,
			expectedStale: true,
		},
		{
			name:          "legacy event",
			current:       3,
			event:         0,
			expectedStale: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			workflow := &workflowspb.GetWorkflowByIDResponse{
				Generation: tt.current,
			}
			event := workflowsmodel.WorkflowEvent{
				Generation: tt.event,
			}

			if got := isStaleWorkflowEvent(workflow, &event); got != tt.expectedStale {
				t.Fatalf("isStaleWorkflowEvent() = %v, want %v", got, tt.expectedStale)
			}
		})
	}
}

func TestDeleteWorkflowLogsClickHouseQueryUsesClickHousePlaceholders(t *testing.T) {
	t.Parallel()

	query := deleteWorkflowLogsClickHouseQuery()
	if strings.Contains(query, "$1") || strings.Contains(query, "$2") {
		t.Fatalf("deleteWorkflowLogsClickHouseQuery() uses PostgreSQL placeholders: %s", query)
	}
	if got := strings.Count(query, "?"); got != 2 {
		t.Fatalf("deleteWorkflowLogsClickHouseQuery() placeholder count = %d, want 2: %s", got, query)
	}
	if !strings.Contains(query, "mutations_sync = 2") {
		t.Fatalf("deleteWorkflowLogsClickHouseQuery() must wait for mutation completion: %s", query)
	}
}

func TestBuildWorkflowTransientBuildErrorDoesNotFailWorkflow(t *testing.T) {
	t.Parallel()

	statusUpdates := &orderedEvents{}
	notifications := &orderedEvents{}
	buildErr := status.Error(codes.ResourceExhausted, "timed out waiting for image pull lock")
	repo := newBuildWorkflowTestRepository(t, &buildWorkflowTestOptions{
		workflow: &workflowspb.GetWorkflowByIDResponse{
			Id:          "workflow-1",
			UserId:      "user-1",
			Name:        "workflow",
			Kind:        workflowsmodel.KindContainer.ToString(),
			BuildStatus: workflowsmodel.WorkflowBuildStatusQueued.ToString(),
			Payload:     `{"image":"alpine:3.22","cmd":["echo","hello"]}`,
			Generation:  1,
		},
		buildErr:      buildErr,
		statusUpdates: statusUpdates,
		notifications: notifications,
	})

	err := repo.buildWorkflow(t.Context(), &workflowsmodel.WorkflowEvent{
		ID:         "workflow-1",
		UserID:     "user-1",
		Action:     workflowsmodel.ActionBuild,
		Generation: 1,
	})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("buildWorkflow() code = %s, want %s: %v", status.Code(err), codes.ResourceExhausted, err)
	}

	assertEvents(t, statusUpdates.items(), []string{workflowsmodel.WorkflowBuildStatusStarted.ToString()})
	if got := notifications.items(); containsEvent(got, "Workflow Build Failed") {
		t.Fatalf("failure notification sent for transient build error: %v", got)
	}
}

func TestBuildWorkflowStartedStatusRetriesBuild(t *testing.T) {
	t.Parallel()

	builds := &orderedEvents{}
	statusUpdates := &orderedEvents{}
	repo := newBuildWorkflowTestRepository(t, &buildWorkflowTestOptions{
		workflow: &workflowspb.GetWorkflowByIDResponse{
			Id:          "workflow-1",
			UserId:      "user-1",
			Name:        "workflow",
			Kind:        workflowsmodel.KindContainer.ToString(),
			BuildStatus: workflowsmodel.WorkflowBuildStatusStarted.ToString(),
			Payload:     `{"image":"alpine:3.22","cmd":["echo","hello"]}`,
			Generation:  1,
		},
		builds:        builds,
		statusUpdates: statusUpdates,
	})

	err := repo.buildWorkflow(t.Context(), &workflowsmodel.WorkflowEvent{
		ID:         "workflow-1",
		UserID:     "user-1",
		Action:     workflowsmodel.ActionBuild,
		Generation: 1,
	})
	if err != nil {
		t.Fatalf("buildWorkflow() error = %v", err)
	}

	assertEvents(t, builds.items(), []string{"alpine:3.22"})
	assertEvents(t, statusUpdates.items(), []string{workflowsmodel.WorkflowBuildStatusCompleted.ToString()})
}

func TestBuildWorkflowNonTransientBuildErrorFailsWorkflow(t *testing.T) {
	t.Parallel()

	statusUpdates := &orderedEvents{}
	notifications := &orderedEvents{}
	buildErr := status.Error(codes.NotFound, "failed to pull image")
	repo := newBuildWorkflowTestRepository(t, &buildWorkflowTestOptions{
		workflow: &workflowspb.GetWorkflowByIDResponse{
			Id:          "workflow-1",
			UserId:      "user-1",
			Name:        "workflow",
			Kind:        workflowsmodel.KindContainer.ToString(),
			BuildStatus: workflowsmodel.WorkflowBuildStatusQueued.ToString(),
			Payload:     `{"image":"missing:latest","cmd":["echo","hello"]}`,
			Generation:  1,
		},
		buildErr:      buildErr,
		statusUpdates: statusUpdates,
		notifications: notifications,
	})

	err := repo.buildWorkflow(t.Context(), &workflowsmodel.WorkflowEvent{
		ID:         "workflow-1",
		UserID:     "user-1",
		Action:     workflowsmodel.ActionBuild,
		Generation: 1,
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("buildWorkflow() code = %s, want %s: %v", status.Code(err), codes.NotFound, err)
	}

	assertEvents(t, statusUpdates.items(), []string{
		workflowsmodel.WorkflowBuildStatusStarted.ToString(),
		workflowsmodel.WorkflowBuildStatusFailed.ToString(),
	})
	if !eventuallyContainsEvent(notifications, "Workflow Build Failed") {
		t.Fatalf("failure notification was not sent for non-transient build error: %v", notifications.items())
	}
}

func TestCancelJobsMarksAllJobsCanceledBeforeContainerCleanup(t *testing.T) {
	t.Parallel()

	events := &orderedEvents{}
	workflow := &workflowspb.GetWorkflowByIDResponse{
		Id:     "workflow-1",
		UserId: "user-1",
		Kind:   workflowsmodel.KindContainer.ToString(),
	}
	repo := &Repository{
		auth: testAuth{},
		svc: &Services{
			Jobs: &testJobsClient{
				updateJobStatus: func(_ context.Context, req *jobspb.UpdateJobStatusRequest) (*jobspb.UpdateJobStatusResponse, error) {
					if req.GetId() != "job-1" && req.GetId() != "job-2" {
						t.Fatalf("UpdateJobStatus job id = %q, want job-1 or job-2", req.GetId())
					}
					if req.GetStatus() != jobsmodel.JobStatusCanceled.ToString() {
						t.Fatalf("UpdateJobStatus status = %q, want CANCELED", req.GetStatus())
					}
					events.add("cancel-" + req.GetId())
					return &jobspb.UpdateJobStatusResponse{}, nil
				},
			},
			Notifications: testNotificationsClient{},
			Csvc:          &testContainerSvc{events: events},
		},
	}

	cleanupJobs, err := repo.cancelJobs(
		t.Context(),
		workflow,
		"user-1",
		&jobspb.ListJobsResponse{
			Jobs: []*jobspb.JobsResponse{
				{
					Id:          "job-1",
					ContainerId: "container-1",
				},
				{
					Id:          "job-2",
					ContainerId: "container-2",
				},
			},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("cancelJobs() error = %v", err)
	}

	if got := events.items(); len(got) != 2 || got[0] != "cancel-job-1" || got[1] != "cancel-job-2" {
		t.Fatalf("event order before cleanup = %v, want all cancel updates before container cleanup", got)
	}
	if len(cleanupJobs) != 2 {
		t.Fatalf("cleanup jobs count = %d, want 2", len(cleanupJobs))
	}

	if err := repo.cleanupCanceledJobContainers(t.Context(), workflow, cleanupJobs); err != nil {
		t.Fatalf("cleanupCanceledJobContainers() error = %v", err)
	}

	if got := events.items(); len(got) < 4 || got[0] != "cancel-job-1" || got[1] != "cancel-job-2" || got[2] != "terminate" {
		t.Fatalf("event order after cleanup = %v, want all cancel updates before container cleanup", got)
	}
}

func TestCancelJobsSkipsCleanupWhenJobAlreadyTerminal(t *testing.T) {
	t.Parallel()

	events := &orderedEvents{}
	repo := &Repository{
		auth: testAuth{},
		svc: &Services{
			Jobs: &testJobsClient{
				updateJobStatus: func(context.Context, *jobspb.UpdateJobStatusRequest) (*jobspb.UpdateJobStatusResponse, error) {
					events.add("cancel")
					return nil, status.Error(codes.FailedPrecondition, "job not cancellable")
				},
			},
			Notifications: testNotificationsClient{},
			Csvc:          &testContainerSvc{events: events},
		},
	}

	cleanupJobs, err := repo.cancelJobs(
		t.Context(),
		&workflowspb.GetWorkflowByIDResponse{
			Id:     "workflow-1",
			UserId: "user-1",
			Kind:   workflowsmodel.KindContainer.ToString(),
		},
		"user-1",
		&jobspb.ListJobsResponse{
			Jobs: []*jobspb.JobsResponse{{
				Id:          "job-1",
				ContainerId: "container-1",
			}},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("cancelJobs() error = %v", err)
	}

	if len(cleanupJobs) != 0 {
		t.Fatalf("cleanup jobs count = %d, want 0", len(cleanupJobs))
	}
	if got := events.items(); len(got) != 1 || got[0] != "cancel" {
		t.Fatalf("event order = %v, want only cancel attempt", got)
	}
}

func TestCleanupCanceledJobContainerRemovesContainerWhenLogReplayFails(t *testing.T) {
	t.Parallel()

	events := &orderedEvents{}
	repo := &Repository{
		svc: &Services{
			Csvc: &testContainerSvc{
				events: events,
				logs: []*jobsmodel.JobLog{{
					Message:     "canceled log",
					Stream:      "stdout",
					SequenceNum: 1,
				}},
			},
		},
	}

	err := repo.cleanupCanceledJobContainer(
		t.Context(),
		&workflowspb.GetWorkflowByIDResponse{
			Id:   "workflow-1",
			Kind: workflowsmodel.KindContainer.ToString(),
		},
		&jobspb.JobsResponse{
			Id:          "job-1",
			ContainerId: "container-1",
		},
	)
	if err != nil {
		t.Fatalf("cleanupCanceledJobContainer() error = %v", err)
	}

	if got := events.items(); len(got) != 2 || got[0] != "terminate" || got[1] != "remove" {
		t.Fatalf("cleanup events = %v, want terminate then remove", got)
	}
}

func TestCancelJobsIgnoresNotificationAuthorizationFailure(t *testing.T) {
	issueTokenCalls := 0
	repo := &Repository{
		auth: testAuth{
			issueToken: func(context.Context, string) (string, error) {
				issueTokenCalls++
				if issueTokenCalls == 1 {
					return "token", nil
				}
				return "", status.Error(codes.Unavailable, "auth unavailable")
			},
		},
		svc: &Services{
			Jobs: &testJobsClient{
				updateJobStatus: func(context.Context, *jobspb.UpdateJobStatusRequest) (*jobspb.UpdateJobStatusResponse, error) {
					return &jobspb.UpdateJobStatusResponse{}, nil
				},
			},
			Notifications: testNotificationsClient{},
			Csvc:          &testContainerSvc{events: &orderedEvents{}},
		},
	}

	cleanupJobs, err := repo.cancelJobs(
		t.Context(),
		&workflowspb.GetWorkflowByIDResponse{
			Id:     "workflow-1",
			UserId: "user-1",
			Kind:   workflowsmodel.KindContainer.ToString(),
		},
		"user-1",
		&jobspb.ListJobsResponse{
			Jobs: []*jobspb.JobsResponse{{
				Id:          "job-1",
				ContainerId: "container-1",
			}},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("cancelJobs() error = %v", err)
	}
	if len(cleanupJobs) != 1 {
		t.Fatalf("cleanup jobs count = %d, want 1", len(cleanupJobs))
	}
}

func TestSendWorkflowTerminatedNotificationIgnoresAuthorizationFailure(t *testing.T) {
	t.Parallel()

	repo := &Repository{
		auth: testAuth{
			issueToken: func(context.Context, string) (string, error) {
				return "", status.Error(codes.Unavailable, "auth unavailable")
			},
		},
		svc: &Services{
			Notifications: testNotificationsClient{
				createNotification: func(context.Context, *notificationspb.CreateNotificationRequest) (*notificationspb.CreateNotificationResponse, error) {
					t.Fatal("CreateNotification should not be called when authorization fails")
					return nil, status.Error(codes.Internal, "unexpected notification call")
				},
			},
		},
	}

	repo.sendWorkflowTerminatedNotification(
		t.Context(),
		"user-1",
		&workflowspb.GetWorkflowByIDResponse{
			Id:   "workflow-1",
			Name: "workflow",
		},
		&workflowsmodel.WorkflowEvent{
			ID:         "workflow-1",
			UserID:     "user-1",
			Action:     workflowsmodel.ActionTerminate,
			Generation: 1,
		},
	)
}

type orderedEvents struct {
	mu     sync.Mutex
	events []string
}

func (e *orderedEvents) add(event string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, event)
}

func (e *orderedEvents) items() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.events...)
}

func assertEvents(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %v, want %v", got, want)
		}
	}
}

func containsEvent(events []string, event string) bool {
	for _, item := range events {
		if item == event {
			return true
		}
	}
	return false
}

func eventuallyContainsEvent(events *orderedEvents, event string) bool {
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if containsEvent(events.items(), event) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return containsEvent(events.items(), event)
}

type buildWorkflowTestOptions struct {
	workflow      *workflowspb.GetWorkflowByIDResponse
	buildErr      error
	builds        *orderedEvents
	statusUpdates *orderedEvents
	notifications *orderedEvents
}

func newBuildWorkflowTestRepository(t *testing.T, opts *buildWorkflowTestOptions) *Repository {
	t.Helper()
	if opts == nil {
		opts = &buildWorkflowTestOptions{}
	}
	if opts.builds == nil {
		opts.builds = &orderedEvents{}
	}
	if opts.statusUpdates == nil {
		opts.statusUpdates = &orderedEvents{}
	}
	if opts.notifications == nil {
		opts.notifications = &orderedEvents{}
	}

	return &Repository{
		auth: testAuth{},
		rdb:  &testWorkflowLockStore{},
		kfk:  testKafkaProducer{},
		svc: &Services{
			Workflows: &testWorkflowsClient{
				workflow:      opts.workflow,
				statusUpdates: opts.statusUpdates,
			},
			Jobs:          &testJobsClient{},
			Notifications: testNotificationsClient{events: opts.notifications},
			Csvc: &testContainerSvc{
				buildErr: opts.buildErr,
				builds:   opts.builds,
			},
		},
	}
}

type testAuth struct {
	issueToken func(context.Context, string) (string, error)
}

func (a testAuth) IssueToken(ctx context.Context, subject string) (string, error) {
	if a.issueToken != nil {
		return a.issueToken(ctx, subject)
	}
	return "token", nil
}

func (testAuth) ValidateToken(context.Context) (*jwt.Token, error) {
	return &jwt.Token{Valid: true}, nil
}

type testJobsClient struct {
	jobspb.JobsServiceClient
	updateJobStatus func(context.Context, *jobspb.UpdateJobStatusRequest) (*jobspb.UpdateJobStatusResponse, error)
	scheduleJob     func(context.Context, *jobspb.ScheduleJobRequest) (*jobspb.ScheduleJobResponse, error)
	listJobs        func(context.Context, *jobspb.ListJobsRequest) (*jobspb.ListJobsResponse, error)
}

func (c *testJobsClient) UpdateJobStatus(ctx context.Context, req *jobspb.UpdateJobStatusRequest, _ ...grpc.CallOption) (*jobspb.UpdateJobStatusResponse, error) {
	if c.updateJobStatus == nil {
		return &jobspb.UpdateJobStatusResponse{}, nil
	}
	return c.updateJobStatus(ctx, req)
}

func (c *testJobsClient) ScheduleJob(ctx context.Context, req *jobspb.ScheduleJobRequest, _ ...grpc.CallOption) (*jobspb.ScheduleJobResponse, error) {
	if c.scheduleJob != nil {
		return c.scheduleJob(ctx, req)
	}
	return &jobspb.ScheduleJobResponse{Id: "job-1"}, nil
}

func (c *testJobsClient) ListJobs(ctx context.Context, req *jobspb.ListJobsRequest, _ ...grpc.CallOption) (*jobspb.ListJobsResponse, error) {
	if c.listJobs != nil {
		return c.listJobs(ctx, req)
	}
	return &jobspb.ListJobsResponse{}, nil
}

type testWorkflowsClient struct {
	workflowspb.WorkflowsServiceClient
	workflow      *workflowspb.GetWorkflowByIDResponse
	statusUpdates *orderedEvents
}

func (c *testWorkflowsClient) GetWorkflowByID(context.Context, *workflowspb.GetWorkflowByIDRequest, ...grpc.CallOption) (*workflowspb.GetWorkflowByIDResponse, error) {
	if c.workflow == nil {
		return nil, status.Error(codes.NotFound, "workflow not found")
	}
	return c.workflow, nil
}

func (c *testWorkflowsClient) UpdateWorkflowBuildStatus(
	_ context.Context,
	req *workflowspb.UpdateWorkflowBuildStatusRequest,
	_ ...grpc.CallOption,
) (*workflowspb.UpdateWorkflowBuildStatusResponse, error) {
	if c.statusUpdates != nil {
		c.statusUpdates.add(req.GetBuildStatus())
	}
	if c.workflow != nil {
		c.workflow.BuildStatus = req.GetBuildStatus()
	}
	return &workflowspb.UpdateWorkflowBuildStatusResponse{}, nil
}

type testNotificationsClient struct {
	notificationspb.NotificationsServiceClient
	createNotification func(context.Context, *notificationspb.CreateNotificationRequest) (*notificationspb.CreateNotificationResponse, error)
	events             *orderedEvents
}

func (c testNotificationsClient) CreateNotification(ctx context.Context, req *notificationspb.CreateNotificationRequest, _ ...grpc.CallOption) (*notificationspb.CreateNotificationResponse, error) {
	if c.createNotification != nil {
		return c.createNotification(ctx, req)
	}
	if c.events != nil {
		c.events.add(notificationTitle(req.GetPayload()))
	}
	return &notificationspb.CreateNotificationResponse{Id: "notification-1"}, nil
}

func notificationTitle(payload string) string {
	var data map[string]any
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		return payload
	}
	title, ok := data["title"].(string)
	if !ok {
		return payload
	}
	return title
}

type testContainerSvc struct {
	events      *orderedEvents
	logs        []*jobsmodel.JobLog
	imageExists bool
	dockerHost  string
	buildErr    error
	builds      *orderedEvents
}

func (s *testContainerSvc) Build(_ context.Context, image string) error {
	if s.builds != nil {
		s.builds.add(image)
	}
	return s.buildErr
}

func (s *testContainerSvc) ImageExists(context.Context, string) (bool, error) {
	return s.imageExists, nil
}

func (s *testContainerSvc) DockerHost() string {
	if s.dockerHost != "" {
		return s.dockerHost
	}
	return "tcp://docker-proxy:2375"
}

func (s *testContainerSvc) Logs(context.Context, string) (logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	logsCh := make(chan *jobsmodel.JobLog)
	errsCh := make(chan error)
	go func() {
		defer close(logsCh)
		defer close(errsCh)
		for _, log := range s.logs {
			logsCh <- log
		}
	}()
	return logsCh, errsCh, nil
}

func (s *testContainerSvc) Remove(context.Context, string) error {
	if s.events != nil {
		s.events.add("remove")
	}
	return nil
}

func (s *testContainerSvc) Terminate(context.Context, string) error {
	if s.events != nil {
		s.events.add("terminate")
	}
	return nil
}

type testWorkflowLockStore struct{}

func (*testWorkflowLockStore) AcquireDistributedLock(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}

func (*testWorkflowLockStore) ReleaseDistributedLock(context.Context, string) error {
	return nil
}

type testKafkaProducer struct{}

func (testKafkaProducer) ProduceSync(context.Context, ...*kgo.Record) kgo.ProduceResults {
	return nil
}
