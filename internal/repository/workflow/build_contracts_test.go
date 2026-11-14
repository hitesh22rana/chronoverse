//nolint:testpackage // Verifies worker lifecycle contracts without exporting helpers.
package workflow

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

const (
	buildContractWorkflowID = "00000000-0000-4000-8000-000000000101"
	buildContractUserID     = "00000000-0000-4000-8000-000000000102"
)

func buildContractWorkflow() *workflowspb.GetWorkflowByIDResponse {
	workflow := lifecycleWorkflow()
	workflow.Id, workflow.UserId = buildContractWorkflowID, buildContractUserID
	return workflow
}

func buildContractEvent() *workflowsmodel.WorkflowEvent {
	event := lifecycleEvent(workflowsmodel.ActionBuild)
	event.ID, event.UserID = buildContractWorkflowID, buildContractUserID
	return event
}

type buildContractClient struct {
	workflowspb.WorkflowsServiceClient
	get    func(context.Context, *workflowspb.GetWorkflowByIDRequest) (*workflowspb.GetWorkflowByIDResponse, error)
	update func(context.Context, *workflowspb.UpdateWorkflowBuildStatusRequest) error
}

func (c buildContractClient) GetWorkflowByID(ctx context.Context, req *workflowspb.GetWorkflowByIDRequest, _ ...grpc.CallOption) (*workflowspb.GetWorkflowByIDResponse, error) {
	return c.get(ctx, req)
}

func (c buildContractClient) UpdateWorkflowBuildStatus(
	ctx context.Context, req *workflowspb.UpdateWorkflowBuildStatusRequest, _ ...grpc.CallOption,
) (*workflowspb.UpdateWorkflowBuildStatusResponse, error) {
	if err := c.update(ctx, req); err != nil {
		return nil, err
	}
	return &workflowspb.UpdateWorkflowBuildStatusResponse{}, nil
}

func TestSkippedBuildCompletesBeforeSchedulingAndNotifying(t *testing.T) {
	failure := status.Error(codes.Unavailable, "dependency unavailable")
	for _, tc := range []struct {
		name                                       string
		updateErr, getErr, scheduleErr, wantErr    error
		stale, terminated                          bool
		wantGets, wantSchedules, wantNotifications int
	}{
		{name: "success", wantGets: 1, wantSchedules: 1, wantNotifications: 1},
		{name: "completion failure", updateErr: failure, wantErr: failure},
		{name: "completion generation race", updateErr: status.Error(codes.FailedPrecondition, "stale generation")},
		{name: "reload failure", getErr: failure, wantErr: failure, wantGets: 1},
		{name: "new generation after completion", stale: true, wantGets: 1},
		{name: "terminated after completion", terminated: true, wantGets: 1},
		{name: "schedule failure", scheduleErr: failure, wantErr: failure, wantGets: 1, wantSchedules: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				workflow := buildContractWorkflow()
				workflow.BuildStatus = workflowsmodel.WorkflowBuildStatusQueued.ToString()
				event := buildContractEvent()
				effectKey := event.EventKey
				scheduledKey := idempotency.AutomaticScheduleEventKey(effectKey)
				updates, gets, schedules := 0, 0, 0
				notifications := make(chan *notificationspb.CreateNotificationRequest, 1)
				client := buildContractClient{
					update: func(_ context.Context, req *workflowspb.UpdateWorkflowBuildStatusRequest) error {
						updates++
						assertSkippedCompletion(t, req, workflow, event)
						return tc.updateErr
					},
					get: func(_ context.Context, req *workflowspb.GetWorkflowByIDRequest) (*workflowspb.GetWorkflowByIDResponse, error) {
						gets++
						if updates != 1 || req.GetId() != workflow.GetId() {
							t.Errorf("reload before completion or wrong workflow: %v", req)
						}
						if tc.getErr != nil {
							return nil, tc.getErr
						}
						refreshed := buildContractWorkflow()
						if tc.stale {
							refreshed.Generation++
						}
						if tc.terminated {
							refreshed.TerminatedAt = time.Now().Format(time.RFC3339Nano)
						}
						return refreshed, nil
					},
				}
				r := &Repository{svc: &Services{Workflows: client, Jobs: &testJobsClient{scheduleJob: func(_ context.Context, req *jobspb.ScheduleJobRequest) (*jobspb.ScheduleJobResponse, error) {
					schedules++
					if gets != 1 {
						t.Error("schedule ran before workflow reload")
					}
					assertSkippedSchedule(t, req, workflow, event, scheduledKey)
					if tc.scheduleErr != nil {
						return nil, tc.scheduleErr
					}
					return &jobspb.ScheduleJobResponse{Id: "00000000-0000-4000-8000-000000000103"}, nil
				}}, Notifications: testNotificationsClient{createNotification: func(_ context.Context, req *notificationspb.CreateNotificationRequest) (*notificationspb.CreateNotificationResponse, error) {
					notifications <- req
					return &notificationspb.CreateNotificationResponse{Id: "00000000-0000-4000-8000-000000000104"}, nil
				}}}}
				err := r.completeSkippedWorkflowBuild(t.Context(), t.Context(), workflow, event, scheduledKey, effectKey)
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("completion error=%v want %v", err, tc.wantErr)
				}
				synctest.Wait()
				if updates != 1 || gets != tc.wantGets || schedules != tc.wantSchedules || len(notifications) != tc.wantNotifications {
					t.Fatalf("effects update/get/schedule/notifications=%d/%d/%d/%d", updates, gets, schedules, len(notifications))
				}
				if tc.wantNotifications == 1 {
					req := <-notifications
					assertSkippedNotification(t, req, workflow.GetId(), workflow.GetUserId(), effectKey)
				}
			})
		})
	}
}

type prefetchContractContainer struct {
	ContainerSvc
	build func(context.Context, string) error
}

func (c prefetchContractContainer) Build(ctx context.Context, image string) error {
	return c.build(ctx, image)
}

type prefetchContextKey struct{}

func TestImagePrefetchDetachesCancellationPreservesValuesAndBoundsLifetime(t *testing.T) {
	const digest = "image@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, tc := range []struct {
		name, ref, digest, want string
		timeout, wantTimeout    time.Duration
	}{
		{"immutable digest", "image:tag", digest, digest, time.Second, time.Second},
		{"reference fallback", "image:tag", "", "image:tag", time.Second, time.Second},
		{"default lifetime", "image:tag", digest, digest, 0, defaultImagePrefetchTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				parent, cancel := context.WithCancel(context.WithValue(t.Context(), prefetchContextKey{}, "trace-context"))
				cancel()
				started := make(chan context.Context, 1)
				finished := make(chan error, 1)
				repo := &Repository{auth: testAuth{}, svc: &Services{
					ImagePrefetch: ImagePrefetchConfig{Enabled: true, MaxFanout: 1, Timeout: tc.timeout},
					Jobs: &testJobsClient{listReadyRuntimeNodes: func(
						ctx context.Context, _ *jobspb.ListReadyRuntimeNodesRequest,
					) (*jobspb.ListReadyRuntimeNodesResponse, error) {
						if ctx.Err() != nil || ctx.Value(prefetchContextKey{}) != "trace-context" {
							t.Errorf("prefetch lost detached context: err=%v value=%v", ctx.Err(), ctx.Value(prefetchContextKey{}))
						}
						return &jobspb.ListReadyRuntimeNodesResponse{Nodes: []*jobspb.GetReadyRuntimeNodeResponse{{RuntimeNodeId: "other-node", RuntimeEndpoint: "tcp://other:2376"}}}, nil
					}}, CsvcForEndpoint: func(node, endpoint string) (ContainerSvc, error) {
						if node != "other-node" || endpoint != "tcp://other:2376" {
							t.Errorf("incorrect runtime route: %q %q", node, endpoint)
						}
						return prefetchContractContainer{build: func(ctx context.Context, image string) error {
							if image != tc.want {
								t.Errorf("prefetch image=%q want %q", image, tc.want)
							}
							started <- ctx
							<-ctx.Done()
							finished <- ctx.Err()
							return ctx.Err()
						}}, nil
					},
				}}
				before := time.Now()
				repo.startImagePrefetch(parent, buildContractWorkflowID, tc.ref, tc.digest, "warm-node")
				synctest.Wait()
				if len(started) != 1 {
					t.Fatal("prefetch did not start")
				}
				ctx := <-started
				deadline, ok := ctx.Deadline()
				if !ok || !deadline.Equal(before.Add(tc.wantTimeout)) {
					t.Fatalf("deadline=%v want %v", deadline, before.Add(tc.wantTimeout))
				}
				if ctx.Err() != nil {
					t.Fatalf("parent cancellation leaked to prefetch: %v", ctx.Err())
				}
				time.Sleep(tc.wantTimeout)
				synctest.Wait()
				if len(finished) != 1 || !errors.Is(<-finished, context.DeadlineExceeded) {
					t.Fatal("prefetch did not join the timed-out build")
				}
			})
		})
	}
}

func TestImagePrefetchSkipsDisabledEmptyAndUnauthorizedRequests(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		image   string
		authErr error
	}{
		{name: "disabled", image: "image:tag"},
		{name: "empty image", enabled: true},
		{name: "authorization failure", enabled: true, image: "image:tag", authErr: errors.New("signer unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := make(chan struct{}, 1)
				r := &Repository{auth: testAuth{issueToken: func(context.Context, string) (string, error) { return "", tc.authErr }}, svc: &Services{
					ImagePrefetch: ImagePrefetchConfig{Enabled: tc.enabled},
					Jobs: &testJobsClient{listReadyRuntimeNodes: func(
						context.Context, *jobspb.ListReadyRuntimeNodesRequest,
					) (*jobspb.ListReadyRuntimeNodesResponse, error) {
						calls <- struct{}{}
						return &jobspb.ListReadyRuntimeNodesResponse{}, nil
					}},
				}}
				r.startImagePrefetch(t.Context(), buildContractWorkflowID, tc.image, "", "warm-node")
				synctest.Wait()
				if len(calls) != 0 {
					t.Fatal("prefetch accessed runtime registry despite guard failure")
				}
			})
		})
	}
}

func TestImagePrefetchContinuesAfterTargetFailuresAndCapsDefaultFanout(t *testing.T) {
	for _, tc := range []struct {
		name                                      string
		listFailure, factoryFailure, buildFailure bool
		fanout, wantFactories, wantBuilds         int
	}{
		{name: "registry unavailable", listFailure: true},
		{name: "factory failure", factoryFailure: true, fanout: 3, wantFactories: 3, wantBuilds: 2},
		{name: "build failure", buildFailure: true, fanout: 3, wantFactories: 3, wantBuilds: 3},
		{name: "default cap", wantFactories: defaultImagePrefetchMaxFanout, wantBuilds: defaultImagePrefetchMaxFanout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := status.Error(codes.Unavailable, "runtime unavailable")
			factories := 0
			builds := make(chan string, 10)
			r := &Repository{svc: &Services{
				ImagePrefetch: ImagePrefetchConfig{Enabled: true, MaxFanout: tc.fanout},
				Jobs: &testJobsClient{listReadyRuntimeNodes: func(
					context.Context, *jobspb.ListReadyRuntimeNodesRequest,
				) (*jobspb.ListReadyRuntimeNodesResponse, error) {
					if tc.listFailure {
						return nil, failure
					}
					nodes := []*jobspb.GetReadyRuntimeNodeResponse{{RuntimeNodeId: "warm-node", RuntimeEndpoint: "tcp://warm:2376"}}
					for i := range 10 {
						nodes = append(nodes, &jobspb.GetReadyRuntimeNodeResponse{RuntimeNodeId: fmt.Sprintf("node-%d", i), RuntimeEndpoint: fmt.Sprintf("tcp://node-%d:2376", i)})
					}
					return &jobspb.ListReadyRuntimeNodesResponse{Nodes: nodes}, nil
				}}, CsvcForEndpoint: func(node, endpoint string) (ContainerSvc, error) {
					factories++
					if node == "warm-node" || endpoint == "" {
						t.Errorf("incorrect prefetch target %q/%q", node, endpoint)
					}
					if tc.factoryFailure && node == "node-0" {
						return nil, failure
					}
					return prefetchContractContainer{build: func(ctx context.Context, image string) error {
						if image != "image:tag" || ctx.Err() != nil {
							t.Errorf("incorrect prefetch build image/context: %q/%v", image, ctx.Err())
						}
						builds <- node
						if tc.buildFailure {
							return failure
						}
						return nil
					}}, nil
				},
			}}
			r.prefetchImageToNodes(t.Context(), "image:tag", "warm-node")
			if factories != tc.wantFactories || len(builds) != tc.wantBuilds {
				t.Fatalf("factories/builds=%d/%d want %d/%d", factories, len(builds), tc.wantFactories, tc.wantBuilds)
			}
			assertPrefetchTargets(t, builds, tc.wantBuilds, tc.factoryFailure)
		})
	}
}

func assertSkippedCompletion(t *testing.T, req *workflowspb.UpdateWorkflowBuildStatusRequest, workflow *workflowspb.GetWorkflowByIDResponse, event *workflowsmodel.WorkflowEvent) {
	t.Helper()
	if req.GetId() != workflow.GetId() || req.GetUserId() != workflow.GetUserId() || req.GetGeneration() != event.Generation {
		t.Errorf("incorrect completion identity: %v", req)
	}
	if req.GetBuildStatus() != "COMPLETED" || req.GetResolvedImageRef() != "" || req.GetResolvedImageDigest() != "" {
		t.Errorf("incorrect heartbeat completion state: %v", req)
	}
}

func assertSkippedSchedule(t *testing.T, req *jobspb.ScheduleJobRequest, workflow *workflowspb.GetWorkflowByIDResponse, event *workflowsmodel.WorkflowEvent, key string) {
	t.Helper()
	if req.GetWorkflowId() != workflow.GetId() || req.GetUserId() != workflow.GetUserId() || req.GetWorkflowGeneration() != event.Generation {
		t.Errorf("incorrect schedule identity: %v", req)
	}
	if req.GetIdempotencyKey() != key || req.GetTrigger() != "AUTOMATIC" {
		t.Errorf("incorrect automatic schedule: %v", req)
	}
	scheduledAt, err := time.Parse(time.RFC3339Nano, req.GetScheduledAt())
	if err != nil || !scheduledAt.Equal(time.Now().Add(5*time.Minute)) {
		t.Errorf("scheduled at %q: %v", req.GetScheduledAt(), err)
	}
}

func assertSkippedNotification(t *testing.T, req *notificationspb.CreateNotificationRequest, workflowID, userID, key string) {
	t.Helper()
	if req.GetUserId() != userID || notificationTitle(req.GetPayload()) != "Workflow Build Skipped" {
		t.Fatalf("incorrect skipped notification: %v", req)
	}
	if req.GetIdempotencyKey() != idempotency.WorkflowNotificationEventKey(workflowID, "Workflow Build Skipped", key) {
		t.Fatalf("incorrect notification identity: %v", req)
	}
}

func assertPrefetchTargets(t *testing.T, builds <-chan string, count int, factoryFailed bool) {
	t.Helper()
	seen := make(map[string]bool)
	for range count {
		node := <-builds
		if seen[node] {
			t.Fatalf("duplicate prefetch for %q", node)
		}
		seen[node] = true
	}
	if factoryFailed && seen["node-0"] {
		t.Fatal("failed factory reached the build")
	}
}

func TestAutomaticScheduleSuppressesPreconditionRacesAndPreservesFailures(t *testing.T) {
	for _, tc := range []struct {
		name          string
		dependencyErr error
		want          codes.Code
	}{
		{name: "scheduled"},
		{name: "generation changed", dependencyErr: status.Error(codes.FailedPrecondition, "generation changed")},
		{name: "dependency unavailable", dependencyErr: status.Error(codes.Unavailable, "jobs unavailable"), want: codes.Unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				workflow := buildContractWorkflow()
				event := buildContractEvent()
				key := idempotency.AutomaticScheduleEventKey(event.EventKey)
				calls := 0
				repo := &Repository{svc: &Services{Jobs: &testJobsClient{scheduleJob: func(_ context.Context, req *jobspb.ScheduleJobRequest) (*jobspb.ScheduleJobResponse, error) {
					calls++
					assertSkippedSchedule(t, req, workflow, event, key)
					if tc.dependencyErr != nil {
						return nil, tc.dependencyErr
					}
					return &jobspb.ScheduleJobResponse{Id: "00000000-0000-4000-8000-000000000103"}, nil
				}}}}
				err := repo.scheduleAutomaticJob(t.Context(), workflow, key)
				if calls != 1 || status.Code(err) != tc.want {
					t.Fatalf("schedule calls=%d error=%v want %v", calls, err, tc.want)
				}
				if tc.want != codes.OK && !errors.Is(err, tc.dependencyErr) {
					t.Fatalf("original dependency failure lost: %v", err)
				}
			})
		})
	}
}
