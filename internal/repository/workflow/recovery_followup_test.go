//nolint:testpackage // Exercises workflow recovery boundaries without exporting helpers.
package workflow

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

func TestTerminalEventRequiresJobIdentityBeforeAuthorization(t *testing.T) {
	repo := &Repository{}
	ctx, workflow, err := repo.loadWorkflowForTerminalJobEvent(t.Context(), buildContractEvent())
	if ctx != nil || workflow != nil || status.Code(err) != codes.InvalidArgument {
		t.Fatalf("load=(%v,%v,%v)", ctx, workflow, err)
	}
}

func TestCancellationPaginationPreservesFilterAndExclusions(t *testing.T) {
	calls, cancels := 0, 0
	repo := &Repository{auth: testAuth{}, svc: &Services{Jobs: &lifecycleJobsClient{
		testJobsClient: &testJobsClient{listJobs: func(_ context.Context, req *jobspb.ListJobsRequest) (*jobspb.ListJobsResponse, error) {
			calls++
			wantCursor := ""
			if calls == 2 {
				wantCursor = "next-page"
			}
			if req.GetCursor() != wantCursor ||
				req.GetWorkflowId() != buildContractWorkflowID ||
				req.GetUserId() != buildContractUserID ||
				req.GetFilters().GetStatus() != "QUEUED" ||
				req.GetFilters().GetTrigger() != "AUTOMATIC" {
				t.Errorf("page request=%v", req)
			}
			if calls == 1 {
				return &jobspb.ListJobsResponse{Jobs: []*jobspb.JobsResponse{{Id: "excluded"}}, Cursor: "next-page"}, nil
			}
			return &jobspb.ListJobsResponse{Jobs: []*jobspb.JobsResponse{{Id: "deleted"}}}, nil
		}},
		cancel: func(_ context.Context, req *jobspb.CancelJobRequest) (*jobspb.CancelJobResponse, error) {
			cancels++
			if req.GetId() != "deleted" {
				t.Errorf("canceled excluded job=%v", req)
			}
			return nil, status.Error(codes.NotFound, "deleted")
		},
	}}}
	cleanup, err := repo.markJobsCanceledWithStatusAndTriggerExcept(t.Context(), buildContractWorkflow(), buildContractUserID, "QUEUED", "AUTOMATIC", "excluded")
	if err != nil || calls != 2 || cancels != 1 || len(cleanup) != 0 {
		t.Fatalf("pages/cancels/cleanup=%d/%d/%v error=%v", calls, cancels, cleanup, err)
	}
}

type failingCleanupContainer struct {
	ContainerSvc
	terminateErr error
	terminates   int
}

func (c *failingCleanupContainer) Terminate(ctx context.Context, id string) error {
	if ctx.Err() != nil || id != "container-recovery" {
		return errors.New("incorrect termination context or target")
	}
	c.terminates++
	return c.terminateErr
}

func TestCanceledContainerCleanupStopsAtRoutingAndTerminationFailure(t *testing.T) {
	for _, stage := range []string{"factory", "terminate"} {
		t.Run(stage, func(t *testing.T) {
			failure := status.Error(codes.Unavailable, "runtime unavailable")
			container := &failingCleanupContainer{terminateErr: failure}
			factories := 0
			repo := &Repository{svc: &Services{CsvcForEndpoint: func(node, endpoint string) (ContainerSvc, error) {
				factories++
				if node != "runtime-node" || endpoint != "tcp://runtime:2376" {
					t.Errorf("runtime route=%s/%s", node, endpoint)
				}
				if stage == "factory" {
					return nil, failure
				}
				return container, nil
			}}}
			workflow := buildContractWorkflow()
			workflow.Kind = "CONTAINER"
			err := repo.cleanupCanceledJobContainer(t.Context(), workflow, &jobspb.JobsResponse{ContainerId: "container-recovery", RuntimeNodeId: "runtime-node", RuntimeEndpoint: "tcp://runtime:2376"})
			wantTerminates := 1
			if stage == "factory" {
				wantTerminates = 0
			}
			if !errors.Is(err, failure) || factories != 1 || container.terminates != wantTerminates {
				t.Fatalf("factories/terminates=%d/%d error=%v", factories, container.terminates, err)
			}
		})
	}
}

func TestContainerBuildDependencyFailuresAndCompletionFences(t *testing.T) {
	for _, stage := range []string{"payload", "runtime registry", "runtime factory", "completion error", "completion superseded"} {
		t.Run(stage, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				workflow := buildContractWorkflow()
				workflow.Kind = "CONTAINER"
				workflow.BuildStatus = "STARTED"
				workflow.Payload = `{"image":"alpine:3.22","cmd":["echo","hello"]}`
				if stage == "payload" {
					workflow.Payload = "{"
				}
				event := buildContractEvent()
				registry, factories, updates := 0, 0, 0
				notifications := make(chan string, 2)
				failure := status.Error(codes.Unavailable, "dependency unavailable")
				client := buildContractClient{update: func(_ context.Context, req *workflowspb.UpdateWorkflowBuildStatusRequest) error {
					updates++
					return followupBuildUpdate(t, stage, req, failure)
				}}
				repo := &Repository{auth: testAuth{}, svc: &Services{
					Workflows: client, Jobs: &testJobsClient{getReadyRuntimeNode: func(context.Context, *jobspb.GetReadyRuntimeNodeRequest) (*jobspb.GetReadyRuntimeNodeResponse, error) {
						registry++
						if stage == "runtime registry" {
							return nil, failure
						}
						return &jobspb.GetReadyRuntimeNodeResponse{RuntimeNodeId: "runtime-node", RuntimeEndpoint: "tcp://runtime:2376"}, nil
					}},
					CsvcForEndpoint: func(node, endpoint string) (ContainerSvc, error) {
						factories++
						if node != "runtime-node" || endpoint != "tcp://runtime:2376" {
							t.Errorf("runtime route=%s/%s", node, endpoint)
						}
						if stage == "runtime factory" {
							return nil, failure
						}
						return &testContainerSvc{}, nil
					},
					Notifications: testNotificationsClient{createNotification: func(_ context.Context, req *notificationspb.CreateNotificationRequest) (*notificationspb.CreateNotificationResponse, error) {
						notifications <- notificationTitle(req.GetPayload())
						return &notificationspb.CreateNotificationResponse{}, nil
					}},
				}}
				err := repo.buildContainerWorkflow(t.Context(), t.Context(), t.Context(), workflow, event, "schedule", "occurrence")
				synctest.Wait()
				want := map[string]struct {
					code   codes.Code
					counts [4]int
				}{
					"payload":               {codes.InvalidArgument, [4]int{0, 0, 1, 2}},
					"runtime registry":      {codes.Unavailable, [4]int{2, 0, 0, 1}},
					"runtime factory":       {codes.Unavailable, [4]int{2, 2, 0, 1}},
					"completion error":      {codes.Unavailable, [4]int{1, 1, 1, 1}},
					"completion superseded": {codes.OK, [4]int{1, 1, 1, 1}},
				}[stage]
				gotCounts := [4]int{registry, factories, updates, len(notifications)}
				if status.Code(err) != want.code || gotCounts != want.counts {
					t.Fatalf("error=%v counts=%v want %v/%v", err, gotCounts, want.code, want.counts)
				}
			})
		})
	}
}

func followupBuildUpdate(t *testing.T, stage string, req *workflowspb.UpdateWorkflowBuildStatusRequest, failure error) error {
	t.Helper()
	if req.GetId() != buildContractWorkflowID || req.GetUserId() != buildContractUserID || req.GetGeneration() != 3 {
		t.Errorf("update identity=%v", req)
	}
	if stage == "payload" {
		if req.GetBuildStatus() != "FAILED" {
			t.Errorf("failed payload status=%v", req)
		}
		return nil
	}
	if req.GetBuildStatus() != "COMPLETED" || req.GetResolvedImageRef() != "alpine:3.22" || req.GetResolvedImageDigest() != "alpine:3.22@sha256:test" {
		t.Errorf("completion status=%v", req)
	}
	if stage == "completion superseded" {
		return status.Error(codes.FailedPrecondition, "generation changed")
	}
	return failure
}

func TestHeartbeatBuildCompletesThroughWorkerEntry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		workflow := buildContractWorkflow()
		workflow.BuildStatus = "QUEUED"
		event := buildContractEvent()
		lock := &lifecycleLock{acquired: true}
		schedules := 0
		notifications := make(chan string, 1)
		producer := &recoveryProducer{}
		repo := &Repository{auth: testAuth{}, rdb: lock, kfk: producer, svc: &Services{
			Workflows: &testWorkflowsClient{workflow: workflow},
			Jobs: &testJobsClient{scheduleJob: func(_ context.Context, req *jobspb.ScheduleJobRequest) (*jobspb.ScheduleJobResponse, error) {
				schedules++
				assertSkippedSchedule(t, req, workflow, event, idempotency.AutomaticScheduleEventKey(workflowOccurrenceKey(event)))
				return &jobspb.ScheduleJobResponse{}, nil
			}},
			Notifications: testNotificationsClient{createNotification: func(_ context.Context, req *notificationspb.CreateNotificationRequest) (*notificationspb.CreateNotificationResponse, error) {
				notifications <- notificationTitle(req.GetPayload())
				return &notificationspb.CreateNotificationResponse{}, nil
			}},
		}}
		if err := repo.buildWorkflow(t.Context(), event); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if workflow.GetBuildStatus() != "COMPLETED" || schedules != 1 || len(producer.records) != 1 || len(notifications) != 1 {
			t.Fatalf("status=%s schedules/analytics/notifications=%d/%d/%d", workflow.GetBuildStatus(), schedules, len(producer.records), len(notifications))
		}
		if title := <-notifications; title != "Workflow Build Skipped" {
			t.Fatalf("notification=%q", title)
		}
		assertLockReleased(t, lock, 1)
	})
}

func TestCanceledContainerCleanupSkipsJobsWithoutContainerWork(t *testing.T) {
	for _, tc := range []struct{ name, kind string }{
		{"heartbeat", "HEARTBEAT"},
		{"container not started", "CONTAINER"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			factoryCalls := 0
			repo := &Repository{svc: &Services{CsvcForEndpoint: func(string, string) (ContainerSvc, error) {
				factoryCalls++
				return nil, errors.New("cleanup must not select a runtime")
			}}}
			workflow := buildContractWorkflow()
			workflow.Kind = tc.kind
			// Valid routing keeps the missing-endpoint fallback from masking this guard.
			job := &jobspb.JobsResponse{
				Id: "00000000-0000-4000-8000-000000000091", ContainerId: "",
				RuntimeNodeId: "00000000-0000-4000-8000-000000000092", RuntimeEndpoint: "http://runtime.example:2375",
			}
			if err := repo.cleanupCanceledJobContainer(t.Context(), workflow, job); err != nil || factoryCalls != 0 {
				t.Fatalf("no-op cleanup error=%v factory calls=%d", err, factoryCalls)
			}
		})
	}
}
