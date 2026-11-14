//nolint:testpackage // Exercises the renewal budget and ownership-loss cancellation under virtual time.
package workflow

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

type renewalLock struct {
	workflowLockStore
	extend func(context.Context, string, string, time.Duration) (bool, error)
}

func (s *renewalLock) ExtendDistributedLockWithToken(ctx context.Context, key, token string, ttl time.Duration) (bool, error) {
	return s.extend(ctx, key, token, ttl)
}

func TestLifecycleLockStopsWorkWhenRenewalFails(t *testing.T) {
	for _, tc := range []struct {
		name   string
		extend func(context.Context) (bool, error)
		code   codes.Code
	}{
		{"ownership lost", func(context.Context) (bool, error) { return false, nil }, codes.Aborted},
		{"Redis unavailable", func(context.Context) (bool, error) { return false, status.Error(codes.Unavailable, "offline") }, codes.Unavailable},
		{"stalled renewal", func(ctx context.Context) (bool, error) { <-ctx.Done(); return false, ctx.Err() }, codes.Unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				base := &lifecycleLock{acquired: true}
				const ttl = 3 * time.Second
				lock := &renewalLock{workflowLockStore: base, extend: func(ctx context.Context, key, token string, gotTTL time.Duration) (bool, error) {
					if key != "workflow:BUILD:one" || token != "owner-token" || gotTTL != ttl {
						t.Errorf("renewal identity = %s/%s/%s", key, token, gotTTL)
					}
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) > ttl/3 {
						t.Error("renewal exceeds its lease budget")
					}
					return tc.extend(ctx)
				}}
				start := time.Now()
				err := (&Repository{rdb: lock}).withLifecycleLock(t.Context(), "workflow:BUILD:one", ttl, func(ctx context.Context) error {
					<-ctx.Done()
					if status.Code(context.Cause(ctx)) != tc.code {
						t.Errorf("work cancellation cause = %v", context.Cause(ctx))
					}
					return nil // A canceled callback cannot turn ownership loss into success.
				})
				if status.Code(err) != tc.code || time.Since(start) >= ttl {
					t.Fatalf("renewal failure = %v after %s", err, time.Since(start))
				}
				assertLockReleased(t, base, 1)
			})
		})
	}
}

func TestLifecycleLockStopsRenewalAfterCompletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := &lifecycleLock{acquired: true}
		calls := 0
		lock := &renewalLock{workflowLockStore: base, extend: func(context.Context, string, string, time.Duration) (bool, error) {
			calls++
			return true, nil
		}}
		const ttl = 3 * time.Second
		err := (&Repository{rdb: lock}).withLifecycleLock(t.Context(), "workflow:BUILD:one", ttl, func(context.Context) error {
			time.Sleep(4 * ttl)
			return nil
		})
		if err != nil || calls == 0 {
			t.Fatalf("renewed work = %v; renewals=%d", err, calls)
		}
		finishedCalls := calls
		time.Sleep(2 * ttl)
		if calls != finishedCalls {
			t.Fatal("renewal continued after release")
		}
		assertLockReleased(t, base, 1)
	})
}

type ownershipLossContainer struct {
	ContainerSvc
	resolve   func(context.Context, string) (string, string, error)
	terminate func(context.Context, string) error
}

func (c ownershipLossContainer) ResolveImageDigest(ctx context.Context, image string) (ref, digest string, err error) {
	return c.resolve(ctx, image)
}

func (c ownershipLossContainer) Terminate(ctx context.Context, id string) error {
	return c.terminate(ctx, id)
}

func TestBuildWorkflowDoesNotCompleteAfterOwnershipLoss(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		workflow := buildContractWorkflow()
		workflow.Kind, workflow.BuildStatus = "CONTAINER", "STARTED"
		workflow.Payload = `{"image":"alpine:3.22","cmd":["echo","hello"]}`
		repo, base := lifecycleRepository(workflow)
		repo.rdb = &renewalLock{workflowLockStore: base, extend: func(context.Context, string, string, time.Duration) (bool, error) { return false, nil }}
		repo.kfk = &recoveryProducer{}
		updates := 0
		repo.svc.Workflows = buildContractClient{
			get: func(context.Context, *workflowspb.GetWorkflowByIDRequest) (*workflowspb.GetWorkflowByIDResponse, error) {
				return workflow, nil
			},
			update: func(context.Context, *workflowspb.UpdateWorkflowBuildStatusRequest) error { updates++; return nil },
		}
		repo.svc.CsvcForEndpoint = func(string, string) (ContainerSvc, error) {
			return ownershipLossContainer{resolve: func(ctx context.Context, image string) (string, string, error) {
				<-ctx.Done()
				return image, "sha256:accepted-before-loss", nil
			}}, nil
		}
		err := repo.buildWorkflow(t.Context(), buildContractEvent())
		if status.Code(err) != codes.Aborted || updates != 0 {
			t.Fatalf("lost build: err=%v status updates=%d", err, updates)
		}
	})
}

func TestContainerCleanupStopsAfterOwnershipLoss(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		workflow := lifecycleWorkflow()
		workflow.Kind = workflowsmodel.KindContainer.ToString()
		repo, base := lifecycleRepository(workflow)
		repo.rdb = &renewalLock{workflowLockStore: base, extend: func(context.Context, string, string, time.Duration) (bool, error) { return false, nil }}
		repo.svc.CsvcForEndpoint = func(string, string) (ContainerSvc, error) {
			// Logs and Remove are deliberately absent: neither may start after ownership is lost.
			return ownershipLossContainer{terminate: func(ctx context.Context, _ string) error { <-ctx.Done(); return nil }}, nil
		}
		err := repo.withLifecycleLock(t.Context(), "workflow:TERMINATE:one", 3*time.Second, func(ctx context.Context) error {
			return repo.cleanupCanceledJobContainer(ctx, workflow, &jobspb.JobsResponse{ContainerId: "container", RuntimeEndpoint: "tcp://runtime:2376"})
		})
		if status.Code(err) != codes.Aborted {
			t.Fatalf("lost cleanup result = %v", err)
		}
	})
}

func TestCanceledCleanupDoesNotStartAnotherContainerOperation(t *testing.T) {
	for _, stage := range []string{"before cleanup", "during replay"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			workflow := lifecycleWorkflow()
			workflow.Kind = "CONTAINER"
			repo := &Repository{svc: &Services{CsvcForEndpoint: func(string, string) (ContainerSvc, error) {
				return ownershipLossContainer{
					ContainerSvc: recoveryContainer{logs: func(context.Context, string) (<-chan *jobsmodel.JobLog, <-chan error, error) {
						cancel()
						return nil, nil, context.Canceled
					}},
					terminate: func(context.Context, string) error { return nil },
				}, nil
			}}}
			if stage == "before cleanup" {
				cancel()
				repo.svc = nil
			}
			err := repo.cleanupCanceledJobContainer(ctx, workflow, &jobspb.JobsResponse{ContainerId: "container", RuntimeEndpoint: "tcp://runtime:2376"})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled cleanup = %v", err)
			}
		})
	}
}

func TestTerminateWorkflowDoesNotNotifySuccessAfterCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		repo, _ := lifecycleRepository(lifecycleWorkflow())
		repo.svc.Notifications = nil // A success notification must not be attempted.
		repo.svc.Jobs = &testJobsClient{listJobs: func(_ context.Context, req *jobspb.ListJobsRequest) (*jobspb.ListJobsResponse, error) {
			if req.GetFilters().GetStatus() == jobsmodel.JobStatusCanceled.ToString() {
				cancel()
			}
			return &jobspb.ListJobsResponse{}, nil
		}}
		err := repo.terminateWorkflow(ctx, lifecycleEvent(workflowsmodel.ActionTerminate))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled termination = %v", err)
		}
		synctest.Wait()
	})
}
