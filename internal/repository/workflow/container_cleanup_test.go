//nolint:testpackage // Exercises the worker dispatch boundary without exporting internals.
package workflow

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	kafkapkg "github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
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
	// No workflow/jobs clients: cleanup must survive deletion of their source rows.
	repo := &Repository{svc: &Services{CsvcForEndpoint: func(node, endpoint string) (ContainerSvc, error) {
		if node != "original-node" || endpoint != "tcp://original:2376" {
			t.Fatalf("lost routing %q/%q", node, endpoint)
		}
		return container, nil
	}}}
	event := &workflowsmodel.WorkflowEvent{
		EventKey: "cleanup", Action: workflowsmodel.ActionContainerCleanup, ID: "deleted-workflow", UserID: "owner", JobID: "job",
		ContainerCleanup: &workflowsmodel.ContainerCleanup{ContainerID: "original-container", RuntimeNodeID: "original-node", RuntimeEndpoint: "tcp://original:2376", Attempts: 2, LogRetention: true},
	}
	err := repo.runTargetWorkflow(t.Context(), event)
	if err == nil || kafkapkg.ShouldCommitOnError(err) || removed != 0 {
		t.Fatalf("failed replay acknowledged/removes=%d: %v", removed, err)
	}
	failReplay = false
	for range 2 {
		if err := repo.runTargetWorkflow(t.Context(), event); err != nil {
			t.Fatal(err)
		}
	}
	if stopped != 3 || removed != 2 {
		t.Fatalf("stop/remove=%d/%d", stopped, removed)
	}
}

func TestContainerCleanupBoundsEachAttempt(t *testing.T) {
	for _, phase := range []string{"stop", "replay", "remove"} {
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
						t.Fatal("removal started after failure")
						return nil
					},
				}
				repo := &Repository{svc: &Services{CsvcForEndpoint: func(string, string) (ContainerSvc, error) { return container, nil }}}
				event := &workflowsmodel.WorkflowEvent{
					EventKey: "cleanup", Action: workflowsmodel.ActionContainerCleanup, ID: "workflow", UserID: "owner", JobID: "job",
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
	repo := &Repository{svc: &Services{CsvcForEndpoint: func(string, string) (ContainerSvc, error) {
		calls++
		return nil, status.Error(codes.FailedPrecondition, "runtime unavailable")
	}}}
	event := &workflowsmodel.WorkflowEvent{EventKey: "cleanup", Action: workflowsmodel.ActionContainerCleanup, ID: "workflow", UserID: "owner", JobID: "job"}
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
