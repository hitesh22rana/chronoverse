//nolint:testpackage // Tests unexported workflow helpers without widening production API.
package workflow

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/golang-jwt/jwt/v5"
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
}

func (c *testJobsClient) UpdateJobStatus(ctx context.Context, req *jobspb.UpdateJobStatusRequest, _ ...grpc.CallOption) (*jobspb.UpdateJobStatusResponse, error) {
	return c.updateJobStatus(ctx, req)
}

type testNotificationsClient struct {
	notificationspb.NotificationsServiceClient
	createNotification func(context.Context, *notificationspb.CreateNotificationRequest) (*notificationspb.CreateNotificationResponse, error)
}

func (c testNotificationsClient) CreateNotification(ctx context.Context, req *notificationspb.CreateNotificationRequest, _ ...grpc.CallOption) (*notificationspb.CreateNotificationResponse, error) {
	if c.createNotification != nil {
		return c.createNotification(ctx, req)
	}
	return &notificationspb.CreateNotificationResponse{Id: "notification-1"}, nil
}

type testContainerSvc struct {
	events *orderedEvents
}

func (*testContainerSvc) Build(context.Context, string) error {
	return nil
}

func (*testContainerSvc) Logs(context.Context, string) (logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	logsCh := make(chan *jobsmodel.JobLog)
	errsCh := make(chan error)
	close(logsCh)
	close(errsCh)
	return logsCh, errsCh, nil
}

func (s *testContainerSvc) Remove(context.Context, string) error {
	s.events.add("remove")
	return nil
}

func (s *testContainerSvc) Terminate(context.Context, string) error {
	s.events.add("terminate")
	return nil
}
