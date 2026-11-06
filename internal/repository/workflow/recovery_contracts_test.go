//nolint:testpackage // Pins private recovery boundaries without widening the worker API.
package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

type recoveryContainer struct {
	ContainerSvc
	logs func(context.Context, string) (<-chan *jobsmodel.JobLog, <-chan error, error)
}

func (c recoveryContainer) Logs(ctx context.Context, id string) (logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	return c.logs(ctx, id)
}

type recoveryProducer struct {
	records []*kgo.Record
	err     error
}

func (p *recoveryProducer) ProduceSync(_ context.Context, records ...*kgo.Record) kgo.ProduceResults {
	p.records = append(p.records, records...)
	return kgo.ProduceResults{{Record: records[0], Err: p.err}}
}

func TestCanceledLogPublicationPreservesAttemptIdentityAndRetention(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		workflow := buildContractWorkflow()
		workflow.LogRetention = true
		job := &jobspb.JobsResponse{Id: "job-recovery", Attempts: 4}
		producer := &recoveryProducer{}
		repo := &Repository{kfk: producer}
		for _, stamp := range []time.Time{{}, time.Date(2025, 2, 3, 4, 5, 6, 0, time.UTC)} {
			log := &jobsmodel.JobLog{Message: "retained output", Stream: "stderr", SequenceNum: 17, Timestamp: stamp}
			if err := repo.publishCanceledJobLog(t.Context(), workflow, job, log); err != nil {
				t.Fatal(err)
			}
			var got jobsmodel.JobLogEvent
			record := producer.records[len(producer.records)-1]
			if err := json.Unmarshal(record.Value, &got); err != nil {
				t.Fatal(err)
			}
			wantStamp := stamp
			if stamp.IsZero() {
				wantStamp = time.Now().UTC()
			}
			want := jobsmodel.JobLogEvent{
				EventKey: idempotency.LogEventKey(job.GetId(), "stderr", 17, 4),
				JobID:    job.GetId(), WorkflowID: workflow.GetId(), UserID: workflow.GetUserId(),
				Message: "retained output", SequenceNum: 17, Stream: "stderr", Retention: true, TimeStamp: wantStamp,
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("log event=%+v want %+v", got, want)
			}

			if string(record.Key) != job.GetId() {
				t.Fatalf("record key=%q", record.Key)
			}
		}
		if err := repo.publishCanceledJobLog(t.Context(), workflow, job, nil); err != nil || len(producer.records) != 2 {
			t.Fatalf("nil log emitted effect: %v", err)
		}
		producer.err = errors.New("broker unavailable")
		if err := repo.publishCanceledJobLog(t.Context(), workflow, job, &jobsmodel.JobLog{}); status.Code(err) != codes.Unavailable {
			t.Fatalf("broker error=%v", err)
		}
		repo.kfk = nil
		if err := repo.publishCanceledJobLog(t.Context(), workflow, job, &jobsmodel.JobLog{}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("missing broker error=%v", err)
		}
		if err := repo.publishCanceledJobLog(t.Context(), workflow, job, &jobsmodel.JobLog{Timestamp: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}); status.Code(err) != codes.Internal {
			t.Fatalf("invalid timestamp error=%v", err)
		}
	})
}

func TestCanceledLogReplayHandlesChannelFailuresAndDetachedDeadline(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		openErr, streamErr      error
		timeout, publishFailure bool
		want                    codes.Code
	}{
		{name: "missing container", openErr: status.Error(codes.NotFound, "gone")},
		{name: "open failure", openErr: status.Error(codes.Unavailable, "runtime unavailable"), want: codes.Unavailable},
		{name: "stream missing", streamErr: status.Error(codes.NotFound, "gone")},
		{name: "stream failure", streamErr: status.Error(codes.DataLoss, "truncated"), want: codes.DataLoss},
		{name: "deadline", timeout: true, want: codes.DeadlineExceeded},
		{name: "publication failure", publishFailure: true, want: codes.Unavailable},
		{name: "closed channels"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				parent, cancel := context.WithCancel(t.Context())
				cancel()
				producer := &recoveryProducer{}
				if tc.publishFailure {
					producer.err = errors.New("broker unavailable")
				}
				repo := &Repository{kfk: producer}
				container := recoveryContainer{logs: func(ctx context.Context, id string) (<-chan *jobsmodel.JobLog, <-chan error, error) {
					if id != "container-recovery" || ctx.Err() != nil {
						t.Errorf("replay identity/context=%q/%v", id, ctx.Err())
					}
					assertReplayDeadline(ctx, t)
					if tc.openErr != nil {
						return nil, nil, tc.openErr
					}
					logs := make(chan *jobsmodel.JobLog, 1)
					errs := make(chan error, 1)
					if tc.timeout {
						return logs, errs, nil
					}
					if tc.streamErr != nil {
						errs <- tc.streamErr
					}
					if tc.publishFailure {
						logs <- &jobsmodel.JobLog{Message: "output"}
					}
					close(logs)
					close(errs)
					return logs, errs, nil
				}}
				before := time.Now()
				err := repo.replayCanceledJobContainerLogs(parent, container, buildContractWorkflow(), &jobspb.JobsResponse{Id: "job-recovery", ContainerId: "container-recovery"})
				if status.Code(err) != tc.want {
					t.Fatalf("replay=%v want %v", err, tc.want)
				}
				if tc.timeout && !time.Now().Equal(before.Add(2*time.Minute)) {
					t.Fatalf("unbounded replay duration=%v", time.Since(before))
				}
				wantRecords := 0
				if tc.publishFailure {
					wantRecords = 1
				}
				if len(producer.records) != wantRecords {
					t.Fatalf("published=%d", len(producer.records))
				}
			})
		})
	}
}

func TestWorkflowRoutingAndOccurrenceHelperContracts(t *testing.T) {
	repo := &Repository{svc: &Services{}}
	if _, err := repo.containerSvcForRuntime("node", "endpoint"); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("missing factory=%v", err)
	}
	if _, err := repo.containerSvcForJob(&jobspb.JobsResponse{}); status.Code(err) != codes.Unavailable {
		t.Fatalf("missing route=%v", err)
	}
	sentinel := errors.New("runtime factory failed")
	calls := 0
	repo.svc.CsvcForEndpoint = func(node, endpoint string) (ContainerSvc, error) {
		calls++
		if node != "node" || endpoint != "tcp://runtime:2376" {
			t.Errorf("route=%s/%s", node, endpoint)
		}
		return nil, sentinel
	}
	if _, err := repo.containerSvcForJob(&jobspb.JobsResponse{RuntimeNodeId: "node", RuntimeEndpoint: "tcp://runtime:2376"}); !errors.Is(err, sentinel) || calls != 1 {
		t.Fatalf("factory calls=%d error=%v", calls, err)
	}
	event := buildContractEvent()
	event.EventKey = "persisted-occurrence"
	if got := workflowOccurrenceKey(event); got != "persisted-occurrence" {
		t.Fatalf("explicit occurrence=%q", got)
	}
	event.EventKey = ""
	if got := workflowOccurrenceKey(event); got != idempotency.WorkflowEventKey(event.ID, event.Action.ToString(), event.Generation) {
		t.Fatalf("legacy occurrence=%q", got)
	}
	for _, kind := range []string{"HEARTBEAT", "CONTAINER", "future-kind"} {
		if got := isBuildStepRequired(kind); got != (kind != workflowsmodel.KindHeartbeat.ToString()) {
			t.Fatalf("kind=%q build=%v", kind, got)
		}
	}
}

func TestBuildEntryGuardsStopBeforeEffects(t *testing.T) {
	failure := status.Error(codes.Unavailable, "workflow service unavailable")
	for _, tc := range []struct {
		name              string
		authErr, getErr   error
		status            string
		stale, terminated bool
		want              codes.Code
	}{
		{name: "authorization", authErr: failure, want: codes.Unavailable},
		{name: "lookup", getErr: failure, want: codes.Unavailable},
		{name: "stale", stale: true},
		{name: "terminated", terminated: true, want: codes.FailedPrecondition},
		{name: "failed build", status: "FAILED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workflow := buildContractWorkflow()
			if tc.status != "" {
				workflow.BuildStatus = tc.status
			}
			if tc.stale {
				workflow.Generation++
			}
			if tc.terminated {
				workflow.TerminatedAt = "2025-01-01T00:00:00Z"
			}
			gets := 0
			repo := &Repository{
				auth: testAuth{issueToken: func(context.Context, string) (string, error) { return "token", tc.authErr }},
				svc: &Services{Workflows: buildContractClient{get: func(context.Context, *workflowspb.GetWorkflowByIDRequest) (*workflowspb.GetWorkflowByIDResponse, error) {
					gets++
					if tc.getErr != nil {
						return nil, tc.getErr
					}
					return workflow, nil
				}}},
			}
			if err := repo.buildWorkflow(t.Context(), buildContractEvent()); status.Code(err) != tc.want {
				t.Fatalf("build error=%v", err)
			}
			wantGets := 1
			if tc.authErr != nil {
				wantGets = 0
			}
			if gets != wantGets {
				t.Fatalf("gets=%d want %d", gets, wantGets)
			}
		})
	}
}

func TestBuildFailurePreservesDependencyErrorAndGenerationFence(t *testing.T) {
	failure := status.Error(codes.InvalidArgument, "invalid image")
	for _, tc := range []struct {
		name      string
		updateErr error
		want      error
	}{
		{name: "update failed", updateErr: status.Error(codes.Unavailable, "workflow unavailable"), want: status.Error(codes.Unavailable, "workflow unavailable")},
		{name: "generation superseded", updateErr: status.Error(codes.FailedPrecondition, "superseded")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workflow := buildContractWorkflow()
			event := buildContractEvent()
			updates := 0
			repo := &Repository{svc: &Services{Workflows: buildContractClient{update: func(_ context.Context, req *workflowspb.UpdateWorkflowBuildStatusRequest) error {
				updates++
				if req.GetId() != event.ID ||
					req.GetUserId() != workflow.GetUserId() ||
					req.GetGeneration() != event.Generation ||
					req.GetBuildStatus() != "FAILED" ||
					req.GetResolvedImageDigest() != "" ||
					req.GetResolvedImageRef() != "" {
					t.Errorf("failure update=%v", req)
				}
				return tc.updateErr
			}}}}
			err := repo.handleWorkflowBuildFailure(t.Context(), t.Context(), workflow, event, "occurrence", failure)
			if status.Code(err) != status.Code(tc.want) || updates != 1 {
				t.Fatalf("updates=%d error=%v", updates, err)
			}
		})
	}
}

func TestTerminalEventDependencyFailuresStopLaterEffects(t *testing.T) {
	for _, action := range []workflowsmodel.Action{workflowsmodel.ActionJobCompleted, workflowsmodel.ActionJobFailed} {
		for _, stage := range []string{"authorization", "lookup", "counter", "notification"} {
			t.Run(action.ToString()+"/"+stage, func(t *testing.T) {
				failure := status.Error(codes.Unavailable, stage+" unavailable")
				workflow := buildContractWorkflow()
				event := buildContractEvent()
				event.Action = action
				event.JobID = "job-terminal"
				counters, notifications := 0, 0
				base := &testWorkflowsClient{
					workflow: workflow,
					incrementFailures: func(context.Context, *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest) (*workflowspb.IncrementWorkflowConsecutiveJobFailuresCountResponse, error) {
						counters++
						if stage == "counter" {
							return nil, failure
						}
						return &workflowspb.IncrementWorkflowConsecutiveJobFailuresCountResponse{ThresholdReached: true}, nil
					},
				}
				var client workflowspb.WorkflowsServiceClient = resetRecordingWorkflowsClient{
					testWorkflowsClient: base,
					resetCounters: func(context.Context, *workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest) error {
						counters++
						if stage == "counter" {
							return failure
						}
						return nil
					},
				}
				if stage == "lookup" {
					client = buildContractClient{get: func(context.Context, *workflowspb.GetWorkflowByIDRequest) (*workflowspb.GetWorkflowByIDResponse, error) {
						return nil, failure
					}}
				}
				repo := &Repository{
					auth: testAuth{issueToken: func(context.Context, string) (string, error) {
						if stage == "authorization" {
							return "", failure
						}
						return "token", nil
					}},
					svc: &Services{Workflows: client, Notifications: testNotificationsClient{
						createNotification: func(context.Context, *notificationspb.CreateNotificationRequest) (*notificationspb.CreateNotificationResponse, error) {
							notifications++
							return nil, failure
						},
					}},
				}
				var err error
				if action == workflowsmodel.ActionJobCompleted {
					err = repo.handleJobCompleted(t.Context(), event)
				} else {
					err = repo.handleJobFailed(t.Context(), event)
				}
				if !errors.Is(err, failure) {
					t.Fatalf("original dependency failure lost: %v", err)
				}
				wantCounters, wantNotifications := 0, 0
				if stage == "counter" || stage == "notification" {
					wantCounters = 1
				}
				if stage == "notification" {
					wantNotifications = 1
				}
				if counters != wantCounters || notifications != wantNotifications {
					t.Fatalf("counter/notification=%d/%d", counters, notifications)
				}
			})
		}
	}
}

func TestNotificationRejectsUnknownEntityWithoutRPC(t *testing.T) {
	repo := &Repository{}
	if err := repo.sendNotification(t.Context(), "user", "workflow", "job", "title", "message", "WEB_INFO", "UNKNOWN", ""); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid entity=%v", err)
	}
}

func TestCancellationAuthorizationAndListFailuresDoNotMutateJobs(t *testing.T) {
	failure := status.Error(codes.Unavailable, "dependency unavailable")
	for _, stage := range []string{"authorization", "list", "cancel"} {
		t.Run(stage, func(t *testing.T) {
			lists, cancels := 0, 0
			repo := &Repository{
				auth: testAuth{issueToken: func(context.Context, string) (string, error) {
					if stage == "authorization" {
						return "", failure
					}
					return "token", nil
				}},
				svc: &Services{Jobs: &testJobsClient{
					listJobs: func(context.Context, *jobspb.ListJobsRequest) (*jobspb.ListJobsResponse, error) {
						lists++
						if stage == "list" {
							return nil, failure
						}
						return &jobspb.ListJobsResponse{Jobs: []*jobspb.JobsResponse{{Id: "job-cancel"}}}, nil
					},
					cancelJob: func(context.Context, *jobspb.CancelJobRequest) error { cancels++; return failure },
				}},
			}
			if err := repo.cancelJobsWithStatusAndTriggerExcept(t.Context(), buildContractWorkflow(), buildContractUserID, "QUEUED", "AUTOMATIC", "", "excluded"); !errors.Is(err, failure) {
				t.Fatalf("cancellation error=%v", err)
			}
			wantLists, wantCancels := 1, 0
			if stage == "authorization" {
				wantLists = 0
			}
			if stage == "cancel" {
				wantCancels = 1
			}
			if lists != wantLists || cancels != wantCancels {
				t.Fatalf("list/cancel=%d/%d", lists, cancels)
			}
		})
	}
	repo := &Repository{
		auth: testAuth{issueToken: func(context.Context, string) (string, error) { return "", failure }},
	}
	if _, err := repo.cancelJobRecord(t.Context(), "job", "WORKFLOW_UPDATED"); !errors.Is(err, failure) {
		t.Fatalf("direct cancellation auth error=%v", err)
	}
}

func TestBuildContainerStartFenceStopsBeforeRuntimeAndNotifications(t *testing.T) {
	for _, code := range []codes.Code{codes.Unavailable, codes.FailedPrecondition} {
		t.Run(code.String(), func(t *testing.T) {
			workflow := buildContractWorkflow()
			workflow.BuildStatus = "QUEUED"
			event := buildContractEvent()
			updates := 0
			repo := &Repository{svc: &Services{Workflows: buildContractClient{update: func(_ context.Context, req *workflowspb.UpdateWorkflowBuildStatusRequest) error {
				updates++
				if req.GetBuildStatus() != "STARTED" ||
					req.GetGeneration() != event.Generation ||
					req.GetId() != event.ID {
					t.Errorf("start update=%v", req)
				}
				return status.Error(code, "start rejected")
			}}}}
			err := repo.buildContainerWorkflow(t.Context(), t.Context(), t.Context(), workflow, event, "schedule", "occurrence")
			want := code
			if code == codes.FailedPrecondition {
				want = codes.OK
			}
			if status.Code(err) != want || updates != 1 {
				t.Fatalf("updates=%d error=%v", updates, err)
			}
		})
	}
}

func TestWorkflowAnalyticsPublicationFailurePreservesOccurrenceIdentity(t *testing.T) {
	producer := &recoveryProducer{err: errors.New("broker offline")}
	repo := &Repository{kfk: producer}
	workflow := buildContractWorkflow()
	if err := repo.publishWorkflowBuildAnalytics(t.Context(), workflow.GetId(), workflow); status.Code(err) != codes.Unavailable {
		t.Fatalf("analytics error=%v", err)
	}
	if len(producer.records) != 1 {
		t.Fatalf("records=%d", len(producer.records))
	}
	var event struct {
		EventKey, UserID, WorkflowID, EventType string
		Data                                    struct{ Kind string }
	}
	if err := json.Unmarshal(producer.records[0].Value, &event); err != nil {
		t.Fatal(err)
	}
	if event.EventKey != idempotency.WorkflowAnalyticsEventKey(workflow.GetId()) ||
		event.UserID != workflow.GetUserId() ||
		event.WorkflowID != workflow.GetId() ||
		event.EventType != "WORKFLOWS" ||
		event.Data.Kind != workflow.GetKind() {
		t.Fatalf("analytics identity=%+v", event)
	}
}

type recoveryCleanupContainer struct {
	recoveryContainer
	removed  int
	replayed int
}

func (*recoveryCleanupContainer) Terminate(context.Context, string) error { return nil }
func (c *recoveryCleanupContainer) Remove(ctx context.Context, id string) error {
	if c.replayed != 1 {
		return errors.New("container removed before log replay")
	}
	if ctx.Err() != nil || id != "container-recovery" {
		return errors.New("incorrect cleanup context or identity")
	}
	c.removed++
	return nil
}

func TestCleanupRemovesContainerAfterReplayFailure(t *testing.T) {
	parent, cancel := context.WithCancel(t.Context())
	cancel()
	workflow := buildContractWorkflow()
	workflow.Kind = "CONTAINER"
	container := &recoveryCleanupContainer{}
	container.logs = func(context.Context, string) (<-chan *jobsmodel.JobLog, <-chan error, error) {
		container.replayed++
		return nil, nil, status.Error(codes.Unavailable, "log stream unavailable")
	}
	repo := &Repository{svc: &Services{CsvcForEndpoint: func(string, string) (ContainerSvc, error) { return container, nil }}}
	if err := repo.cleanupCanceledJobContainer(parent, workflow, &jobspb.JobsResponse{ContainerId: "container-recovery", RuntimeEndpoint: "tcp://runtime:2376"}); err != nil || container.removed != 1 {
		t.Fatalf("removed=%d error=%v", container.removed, err)
	}
}

func TestCancelJobsExcludesSelectedJobAndIgnoresMissingSnapshot(t *testing.T) {
	calls := 0
	repo := &Repository{auth: testAuth{}, svc: &Services{Jobs: &lifecycleJobsClient{cancel: func(_ context.Context, req *jobspb.CancelJobRequest) (*jobspb.CancelJobResponse, error) {
		calls++
		if req.GetId() != "missing" {
			t.Errorf("canceled excluded job: %v", req)
		}
		return nil, status.Error(codes.NotFound, "already deleted")
	}}}}
	cleanup, err := repo.cancelJobs(t.Context(), buildContractWorkflow(), buildContractUserID,
		&jobspb.ListJobsResponse{Jobs: []*jobspb.JobsResponse{{Id: "excluded"}, {Id: "missing"}}},
		map[string]struct{}{"excluded": {}})
	if err != nil || calls != 1 || len(cleanup) != 0 {
		t.Fatalf("calls=%d cleanup=%v error=%v", calls, cleanup, err)
	}
}

func TestCompletedBuildReplayRetriesOnlyAutomaticSchedule(t *testing.T) {
	for _, code := range []codes.Code{codes.OK, codes.FailedPrecondition, codes.Unavailable} {
		t.Run(code.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				workflow := buildContractWorkflow()
				workflow.BuildStatus = "COMPLETED"
				event := buildContractEvent()
				calls := 0
				repo := &Repository{auth: testAuth{}, svc: &Services{Workflows: &testWorkflowsClient{workflow: workflow}, Jobs: &testJobsClient{
					scheduleJob: func(_ context.Context, req *jobspb.ScheduleJobRequest) (*jobspb.ScheduleJobResponse, error) {
						calls++
						assertSkippedSchedule(t, req, workflow, event, idempotency.AutomaticScheduleEventKey(workflowOccurrenceKey(event)))
						if code == codes.OK {
							return &jobspb.ScheduleJobResponse{}, nil
						}
						return nil, status.Error(code, "schedule result")
					},
				}}}
				err := repo.buildWorkflow(t.Context(), event)
				want := code
				if code == codes.FailedPrecondition {
					want = codes.OK
				}
				if calls != 1 || status.Code(err) != want {
					t.Fatalf("schedule calls=%d error=%v", calls, err)
				}
			})
		})
	}
}

func TestBuildPreparationFailuresReleaseLockWithoutCompleting(t *testing.T) {
	for _, stage := range []string{"lock", "QUEUED", "PENDING", "analytics"} {
		t.Run(stage, func(t *testing.T) {
			workflow := buildContractWorkflow()
			workflow.BuildStatus = "QUEUED"
			failure := status.Error(codes.Unavailable, "dependency unavailable")
			lock := &lifecycleLock{acquired: stage != "lock"}
			producer := &recoveryProducer{}
			if stage == "analytics" {
				producer.err = failure
			}
			lists := 0
			repo := &Repository{auth: testAuth{}, rdb: lock, kfk: producer, svc: &Services{Workflows: &testWorkflowsClient{workflow: workflow}, Jobs: &testJobsClient{
				listJobs: func(_ context.Context, req *jobspb.ListJobsRequest) (*jobspb.ListJobsResponse, error) {
					lists++
					if req.GetFilters().GetStatus() == stage {
						return nil, failure
					}
					return &jobspb.ListJobsResponse{}, nil
				},
			}}}
			err := repo.buildWorkflow(t.Context(), buildContractEvent())
			want := codes.Unavailable
			if stage == "lock" {
				want = codes.Aborted
			}
			if status.Code(err) != want || workflow.GetBuildStatus() != "QUEUED" {
				t.Fatalf("build status=%s error=%v", workflow.GetBuildStatus(), err)
			}
			wantReleased := 1
			if stage == "lock" {
				wantReleased = 0
			}
			if len(lock.released) != wantReleased {
				t.Fatalf("lock releases=%d", len(lock.released))
			}
			wantLists := 2
			if stage == "lock" {
				wantLists = 0
			}
			if stage == "QUEUED" {
				wantLists = 1
			}
			if lists != wantLists {
				t.Fatalf("lists=%d want %d", lists, wantLists)
			}
		})
	}
}

func assertReplayDeadline(ctx context.Context, t *testing.T) {
	t.Helper()
	deadline, ok := ctx.Deadline()
	if !ok || !deadline.Equal(time.Now().Add(2*time.Minute)) {
		t.Errorf("replay deadline=%v", deadline)
	}
}
