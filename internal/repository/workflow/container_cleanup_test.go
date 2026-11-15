//nolint:testpackage // Exercises the worker dispatch boundary without exporting internals.
package workflow

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	kafkapkg "github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

type durableCleanupContainer struct {
	ContainerSvc
	stop   func(context.Context, string) error
	logs   func(context.Context, string) (<-chan *jobsmodel.JobLog, <-chan error, error)
	remove func(context.Context, string) error
}

func (c durableCleanupContainer) Terminate(ctx context.Context, id string) error {
	return c.stop(ctx, id)
}

func (c durableCleanupContainer) Logs(ctx context.Context, id string) (logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	return c.logs(ctx, id)
}

func (c durableCleanupContainer) Remove(ctx context.Context, id string) error {
	return c.remove(ctx, id)
}

func TestContainerCleanupRetriesFromSnapshotWithoutWorkflow(t *testing.T) {
	var stopped, removed int
	failReplay := true
	container := durableCleanupContainer{
		stop: func(_ context.Context, id string) error {
			if id != "original-container" {
				t.Fatalf("stopped %q", id)
			}
			stopped++
			return nil
		},
		logs: func(context.Context, string) (<-chan *jobsmodel.JobLog, <-chan error, error) {
			if failReplay {
				return nil, nil, status.Error(codes.FailedPrecondition, "temporary log failure")
			}
			return nil, nil, nil
		},
		remove: func(_ context.Context, id string) error {
			if id != "original-container" {
				t.Fatalf("removed %q", id)
			}
			removed++
			return nil
		},
	}
	// Only receipt RPCs are available; cleanup must not read deleted workflow/job rows.
	repo := &Repository{auth: testAuth{}, svc: &Services{Jobs: &testJobsClient{}, CsvcForEndpoint: func(node, endpoint string) (ContainerSvc, error) {
		if node != "original-node" || endpoint != "tcp://original:2376" {
			t.Fatalf("lost routing %q/%q", node, endpoint)
		}
		return container, nil
	}}}
	event := &workflowsmodel.WorkflowEvent{
		CleanupGeneration: 1, EventKey: "cleanup", Action: workflowsmodel.ActionContainerCleanup, ID: "deleted-workflow", UserID: "owner", JobID: "job",
		ContainerCleanup: &workflowsmodel.ContainerCleanup{ContainerID: "original-container", RuntimeNodeID: "original-node", RuntimeEndpoint: "tcp://original:2376", Attempts: 2, LogRetention: true},
	}
	err := repo.runTargetWorkflow(t.Context(), event)
	if err == nil || kafkapkg.ShouldCommitOnError(err) || removed != 0 {
		t.Fatalf("failed replay acknowledged/removes=%d: %v", removed, err)
	}
	failReplay = false
	if err := repo.runTargetWorkflow(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	repo.svc.CsvcForEndpoint = func(string, string) (ContainerSvc, error) {
		t.Fatal("completed cleanup contacted its unavailable original runtime")
		return nil, status.Error(codes.Unavailable, "runtime retired")
	}
	if err := repo.runTargetWorkflow(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	if stopped != 2 || removed != 1 {
		t.Fatalf("stop/remove=%d/%d", stopped, removed)
	}
}

func TestContainerCleanupBoundsEachAttempt(t *testing.T) {
	for _, phase := range []string{"read receipt", "stop", "replay", "remove", "write receipt"} {
		t.Run(phase, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				wait := func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
				container := durableCleanupContainer{
					stop: func(ctx context.Context, _ string) error {
						if phase == "stop" {
							return wait(ctx)
						}
						return nil
					},
					logs: func(ctx context.Context, _ string) (<-chan *jobsmodel.JobLog, <-chan error, error) {
						if phase == "replay" {
							return nil, nil, wait(ctx)
						}
						return nil, nil, nil
					},
					remove: func(ctx context.Context, _ string) error {
						if phase == "remove" {
							return wait(ctx)
						}
						if phase == "write receipt" {
							return nil
						}
						t.Fatal("removal started after failure")
						return nil
					},
				}
				jobs := &testJobsClient{}
				if phase == "read receipt" {
					jobs.getReceipt = func(ctx context.Context, _ *jobspb.ContainerCleanupReceiptRequest) (*jobspb.GetContainerCleanupReceiptResponse, error) {
						return nil, wait(ctx)
					}
				}
				if phase == "write receipt" {
					jobs.recordReceipt = func(ctx context.Context, _ *jobspb.ContainerCleanupReceiptRequest) error { return wait(ctx) }
				}
				repo := &Repository{auth: testAuth{}, svc: &Services{Jobs: jobs, CsvcForEndpoint: func(string, string) (ContainerSvc, error) { return container, nil }}}
				event := &workflowsmodel.WorkflowEvent{
					CleanupGeneration: 1, EventKey: "cleanup", Action: workflowsmodel.ActionContainerCleanup, ID: "workflow", UserID: "owner", JobID: "job",
					ContainerCleanup: &workflowsmodel.ContainerCleanup{ContainerID: "container", RuntimeEndpoint: "runtime", LogRetention: true},
				}
				started := time.Now()
				err := repo.runTargetWorkflow(t.Context(), event)
				if status.Code(err) != codes.DeadlineExceeded || kafkapkg.ShouldCommitOnError(err) || time.Since(started) > 3*time.Minute {
					t.Fatalf("cleanup elapsed=%v err=%v", time.Since(started), err)
				}
			})
		})
	}
}

func TestContainerCleanupRejectsMalformedIntentAndRetriesRoutingFailure(t *testing.T) {
	calls := 0
	repo := &Repository{auth: testAuth{}, svc: &Services{Jobs: &testJobsClient{}, CsvcForEndpoint: func(string, string) (ContainerSvc, error) {
		calls++
		return nil, status.Error(codes.FailedPrecondition, "runtime unavailable")
	}}}
	event := &workflowsmodel.WorkflowEvent{CleanupGeneration: 1, EventKey: "cleanup", Action: workflowsmodel.ActionContainerCleanup, ID: "workflow", UserID: "owner", JobID: "job"}
	for _, snapshot := range []*workflowsmodel.ContainerCleanup{nil, {ContainerID: "container"}} {
		event.ContainerCleanup = snapshot
		if err := repo.runTargetWorkflow(t.Context(), event); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("malformed intent accepted: %v", err)
		}
	}
	if calls != 0 {
		t.Fatal("malformed intent reached runtime")
	}
	event.ContainerCleanup = &workflowsmodel.ContainerCleanup{ContainerID: "container", RuntimeEndpoint: "endpoint"}
	event.CleanupGeneration = 0
	if err := repo.runTargetWorkflow(t.Context(), event); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing cleanup generation accepted: %v", err)
	}
	event.CleanupGeneration = 1
	if err := repo.runTargetWorkflow(t.Context(), event); err == nil || kafkapkg.ShouldCommitOnError(err) {
		t.Fatalf("routing failure acknowledged: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	repo.svc.CsvcForEndpoint = func(string, string) (ContainerSvc, error) {
		cancel()
		return nil, status.Error(codes.Aborted, "interrupted initialization")
	}
	if err := repo.runTargetWorkflow(ctx, event); status.Code(err) != codes.Canceled {
		t.Fatalf("caller cancellation=%v", err)
	}
}

type cleanupReceiptEffects struct{ factories, removes, writes, reads int }

func cleanupReceiptFailureFixture(phase string) (*Repository, *workflowsmodel.WorkflowEvent, *cleanupReceiptEffects) {
	failure := status.Error(codes.Unavailable, "dependency unavailable")
	jobs := &testJobsClient{}
	counts := &cleanupReceiptEffects{}
	if phase == "read" {
		jobs.getReceipt = func(context.Context, *jobspb.ContainerCleanupReceiptRequest) (*jobspb.GetContainerCleanupReceiptResponse, error) {
			return nil, failure
		}
	}
	jobs.recordReceipt = func(context.Context, *jobspb.ContainerCleanupReceiptRequest) error {
		counts.writes++
		if phase == "ambiguous write" {
			jobs.cleanupReceipts.Store("job:original", true)
			return failure
		}
		if phase == "write" {
			return failure
		}
		return nil
	}
	container := durableCleanupContainer{
		stop: func(context.Context, string) error { return nil },
		logs: func(context.Context, string) (<-chan *jobsmodel.JobLog, <-chan error, error) { return nil, nil, nil },
		remove: func(context.Context, string) error {
			counts.removes++
			if phase == "remove" {
				return failure
			}
			return nil
		},
	}
	repo := &Repository{auth: testAuth{}, svc: &Services{Jobs: jobs, CsvcForEndpoint: func(string, string) (ContainerSvc, error) { counts.factories++; return container, nil }}}
	failAuthorizationAt := map[string]int{"auth": 1, "cleanup auth": 2}[phase]
	if failAuthorizationAt > 0 {
		authorizations := 0
		repo.auth = testAuth{issueToken: func(context.Context, string) (string, error) {
			authorizations++
			if authorizations == failAuthorizationAt {
				return "", failure
			}
			return "token", nil
		}}
	}
	if phase == "cleanup auth" {
		jobs.getReceipt = func(context.Context, *jobspb.ContainerCleanupReceiptRequest) (*jobspb.GetContainerCleanupReceiptResponse, error) {
			counts.reads++
			return &jobspb.GetContainerCleanupReceiptResponse{}, nil
		}
	}
	event := &workflowsmodel.WorkflowEvent{
		CleanupGeneration: 1, EventKey: "cleanup", Action: workflowsmodel.ActionContainerCleanup, ID: "workflow", UserID: "user", JobID: "job",
		ContainerCleanup: &workflowsmodel.ContainerCleanup{ContainerID: "original", RuntimeEndpoint: "runtime"},
	}
	return repo, event, counts
}

func TestCleanupReceiptFailuresDoNotAcknowledgeIntent(t *testing.T) {
	for _, phase := range []string{"auth", "cleanup auth", "read", "remove", "write", "ambiguous write"} {
		t.Run(phase, func(t *testing.T) {
			repo, event, counts := cleanupReceiptFailureFixture(phase)
			err := repo.runTargetWorkflow(t.Context(), event)
			if phase == "ambiguous write" {
				if err != nil {
					t.Fatalf("durable completion was not acknowledged: %v", err)
				}
			} else if err == nil || kafkapkg.ShouldCommitOnError(err) {
				t.Fatalf("failed cleanup was acknowledged: %v", err)
			}
			if phase != "ambiguous write" {
				for _, identity := range []string{"job " + event.JobID, "container " + event.ContainerCleanup.ContainerID} {
					if !strings.Contains(err.Error(), identity) {
						t.Fatalf("cleanup retry lost identity %q: %v", identity, err)
					}
				}
			}
			assertCleanupReceiptFailureEffects(t, phase, counts)
			if phase == "ambiguous write" {
				if err := repo.runTargetWorkflow(t.Context(), event); err != nil {
					t.Fatal(err)
				}
				if counts.removes != 1 || counts.factories != 1 || counts.writes != 1 {
					t.Fatal("committed receipt with lost response retried Docker")
				}
			}
		})
	}
}

func assertCleanupReceiptFailureEffects(t *testing.T, phase string, c *cleanupReceiptEffects) {
	t.Helper()
	switch phase {
	case "cleanup auth":
		if c.reads != 0 || c.factories != 0 || c.removes != 0 || c.writes != 0 {
			t.Fatalf("cleanup authorization failure reached receipts or Docker: %+v", c)
		}
	case "auth", "read":
		if c.factories != 0 || c.removes != 0 || c.writes != 0 {
			t.Fatal("receipt admission failure reached Docker or recorded completion")
		}
	case "remove":
		if c.removes != 1 || c.writes != 0 {
			t.Fatal("unconfirmed removal recorded completion")
		}
	default:
		if c.removes != 1 || c.writes != 1 {
			t.Fatalf("removes/writes=%d/%d", c.removes, c.writes)
		}
	}
}

func TestContainerCleanupAcknowledgesOnlyDurableDisposition(t *testing.T) {
	for _, state := range []string{"COMPLETED", "QUARANTINED", "STALE"} {
		t.Run(state, func(t *testing.T) {
			repo, event, _ := cleanupReceiptFailureFixture("remove")
			client, ok := repo.svc.Jobs.(*testJobsClient)
			if !ok {
				t.Fatal("unexpected jobs fixture")
			}
			client.beginCleanup = func(context.Context, *jobspb.ContainerCleanupAttemptRequest) (*jobspb.ContainerCleanupAttemptResponse, error) {
				return &jobspb.ContainerCleanupAttemptResponse{State: state}, nil
			}
			repo.svc.CsvcForEndpoint = func(string, string) (ContainerSvc, error) {
				t.Fatal("non-executable delivery contacted Docker")
				return nil, status.Error(codes.Internal, "unexpected Docker call")
			}
			if err := repo.runTargetWorkflow(t.Context(), event); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestContainerCleanupRequiresPersistedQuarantine(t *testing.T) {
	for _, tc := range []struct {
		state   string
		failure bool
	}{{state: "QUARANTINED"}, {state: "COMPLETED"}, {state: "STALE"}, {failure: true}} {
		reportFailure := tc.failure
		t.Run(fmt.Sprint("persist-disposition/", tc.state, reportFailure), func(t *testing.T) {
			repo, event, counts := cleanupReceiptFailureFixture("remove")
			client, ok := repo.svc.Jobs.(*testJobsClient)
			if !ok {
				t.Fatal("unexpected jobs fixture")
			}
			client.failCleanup = func(ctx context.Context, req *jobspb.ContainerCleanupAttemptRequest) (*jobspb.ContainerCleanupAttemptResponse, error) {
				if req.Token != "owner" || req.Generation != 1 || req.ErrorCode != "Unavailable" || req.Interrupted {
					t.Fatalf("wrong failure identity: %v", req)
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("failure report has no deadline")
				}
				if reportFailure {
					return nil, status.Error(codes.Unavailable, "database unavailable")
				}
				return &jobspb.ContainerCleanupAttemptResponse{State: tc.state}, nil
			}
			err := repo.runTargetWorkflow(t.Context(), event)
			if (err != nil) != reportFailure || counts.removes != 1 {
				t.Fatalf("report failure=%v, err=%v, removes=%d", reportFailure, err, counts.removes)
			}
			if reportFailure && kafkapkg.ShouldCommitOnError(err) {
				t.Fatal("acknowledged unpersisted quarantine")
			}
		})
	}
}

func TestCleanupFailureReportingDetachesAndBoundsCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repo, event, _ := cleanupReceiptFailureFixture("remove")
		client, ok := repo.svc.Jobs.(*testJobsClient)
		if !ok {
			t.Fatal("unexpected jobs fixture")
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		repo.svc.CsvcForEndpoint = func(string, string) (ContainerSvc, error) {
			cancel()
			return nil, status.Error(codes.Unavailable, "runtime lost")
		}
		reported := false
		client.failCleanup = func(reportCtx context.Context, req *jobspb.ContainerCleanupAttemptRequest) (*jobspb.ContainerCleanupAttemptResponse, error) {
			if !req.Interrupted || reportCtx.Err() != nil {
				t.Fatalf("cancellation prevented reporting: %v/%v", req, reportCtx.Err())
			}
			reported = true
			<-reportCtx.Done()
			return nil, reportCtx.Err()
		}
		started := time.Now()
		err := repo.runTargetWorkflow(ctx, event)
		if !reported || status.Code(err) != codes.Canceled || time.Since(started) != 5*time.Second || kafkapkg.ShouldCommitOnError(err) {
			t.Fatalf("unbounded or acknowledged failed report: %v (%v)", err, time.Since(started))
		}
	})
}

func TestCleanupAdmissionFailureDoesNotAcknowledgeOrContactDocker(t *testing.T) {
	for _, mode := range []string{"RPC failure", "missing token", "missing disposition", "stalled admission", "report authorization", "stalled report"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				repo, event, counts := cleanupAdmissionFailureFixture(t, mode)
				started := time.Now()
				err := repo.runTargetWorkflow(t.Context(), event)
				if err == nil || kafkapkg.ShouldCommitOnError(err) {
					t.Fatalf("unpersisted cleanup was acknowledged: %v", err)
				}
				if mode == "stalled admission" || mode == "stalled report" {
					if status.Code(err) != codes.DeadlineExceeded || time.Since(started) != 5*time.Second {
						t.Fatalf("unbounded RPC: %v / %v", err, time.Since(started))
					}
				}
				wantEffects := 0
				if mode == "report authorization" || mode == "stalled report" {
					wantEffects = 1
				}
				if counts.factories != wantEffects || counts.removes != wantEffects {
					t.Fatalf("unowned cleanup reached Docker: %+v", counts)
				}
			})
		})
	}
}

func cleanupAdmissionFailureFixture(t *testing.T, mode string) (*Repository, *workflowsmodel.WorkflowEvent, *cleanupReceiptEffects) {
	t.Helper()
	repo, event, counts := cleanupReceiptFailureFixture("remove")
	client, ok := repo.svc.Jobs.(*testJobsClient)
	if !ok {
		t.Fatal("unexpected jobs fixture")
	}
	switch mode {
	case "RPC failure":
		client.beginCleanup = func(context.Context, *jobspb.ContainerCleanupAttemptRequest) (*jobspb.ContainerCleanupAttemptResponse, error) {
			return nil, status.Error(codes.InvalidArgument, "invalid intent")
		}
	case "missing token":
		client.beginCleanup = func(context.Context, *jobspb.ContainerCleanupAttemptRequest) (*jobspb.ContainerCleanupAttemptResponse, error) {
			return &jobspb.ContainerCleanupAttemptResponse{State: "PROCESSING"}, nil
		}
	case "missing disposition":
		client.beginCleanup = func(context.Context, *jobspb.ContainerCleanupAttemptRequest) (*jobspb.ContainerCleanupAttemptResponse, error) {
			return &jobspb.ContainerCleanupAttemptResponse{}, nil
		}
	case "stalled admission":
		client.beginCleanup = func(ctx context.Context, _ *jobspb.ContainerCleanupAttemptRequest) (*jobspb.ContainerCleanupAttemptResponse, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}
	case "report authorization":
		calls := 0
		repo.auth = testAuth{issueToken: func(context.Context, string) (string, error) {
			calls++
			if calls == 3 {
				return "", status.Error(codes.Unavailable, "credentials unavailable")
			}
			return "token", nil
		}}
	case "stalled report":
		client.failCleanup = func(ctx context.Context, _ *jobspb.ContainerCleanupAttemptRequest) (*jobspb.ContainerCleanupAttemptResponse, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}
	}
	return repo, event, counts
}
