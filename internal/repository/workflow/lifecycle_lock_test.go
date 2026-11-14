//nolint:testpackage // Exercises the renewal budget and ownership-loss cancellation under virtual time.
package workflow

import (
	"context"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	kafkapkg "github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
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
			if status.Code(err) != codes.Canceled {
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
		if status.Code(err) != codes.Canceled {
			t.Fatalf("canceled termination = %v", err)
		}
		synctest.Wait()
	})
}

func TestLifecycleLockContextErrorsRemainRetryable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
		code    codes.Code
	}{
		{"canceled", context.Canceled, codes.Canceled},
		{"wrapped cancellation", fmt.Errorf("operation: %w", context.Canceled), codes.Canceled},
		{"deadline", context.DeadlineExceeded, codes.DeadlineExceeded},
		{"wrapped deadline", fmt.Errorf("operation: %w", context.DeadlineExceeded), codes.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &Repository{rdb: &lifecycleLock{acquired: true}}
			err := repo.withLifecycleLock(t.Context(), "workflow:BUILD:one", time.Minute, func(context.Context) error { return tc.failure })
			if status.Code(err) != tc.code || kafkapkg.ShouldCommitOnError(err) {
				t.Fatalf("context failure = %v; commit=%v", err, kafkapkg.ShouldCommitOnError(err))
			}
		})
	}
}

func TestLifecycleLockPreservesIndependentWorkFailure(t *testing.T) {
	for _, code := range []codes.Code{codes.InvalidArgument, codes.NotFound, codes.Unavailable, codes.Canceled, codes.DeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				lock := &renewalLock{workflowLockStore: &lifecycleLock{acquired: true}, extend: func(context.Context, string, string, time.Duration) (bool, error) { return false, nil }}
				err := (&Repository{rdb: lock}).withLifecycleLock(t.Context(), "workflow:BUILD:one", 3*time.Second, func(ctx context.Context) error {
					<-ctx.Done()
					return status.Error(code, "work failed")
				})
				want := code
				if code == codes.Canceled || code == codes.DeadlineExceeded {
					want = codes.Aborted
				}
				if status.Code(err) != want {
					t.Fatalf("combined failure = %v, want %s", err, want)
				}
			})
		})
	}
}

func TestLifecycleLockRenewalToleratesTransientStallWithinLeaseBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		renewed := make(chan struct{})
		lock := &renewalLock{workflowLockStore: &lifecycleLock{acquired: true}, extend: func(ctx context.Context, _, _ string, _ time.Duration) (bool, error) {
			timer := time.NewTimer(6 * time.Second)
			defer timer.Stop()
			defer close(renewed)
			select {
			case <-timer.C:
				return true, nil
			case <-ctx.Done():
				return false, ctx.Err()
			}
		}}
		err := (&Repository{rdb: lock}).withLifecycleLock(t.Context(), "workflow:BUILD:one", 30*time.Second, func(context.Context) error { <-renewed; return nil })
		if err != nil {
			t.Fatalf("renewal aborted with lease budget remaining: %v", err)
		}
	})
}

type delayedAcquireLock struct {
	workflowLockStore
	delay time.Duration
}

func (s delayedAcquireLock) AcquireDistributedLockWithToken(ctx context.Context, key string, ttl time.Duration) (token string, acquired bool, err error) {
	time.Sleep(s.delay)
	return s.workflowLockStore.AcquireDistributedLockWithToken(ctx, key, ttl)
}

func TestLifecycleLockRejectsInvalidTTL(t *testing.T) {
	for _, ttl := range []time.Duration{-time.Second, 0, time.Nanosecond, 2 * time.Nanosecond} {
		err := (&Repository{}).withLifecycleLock(t.Context(), "workflow:BUILD:one", ttl, func(context.Context) error {
			t.Fatal("invalid TTL entered protected work")
			return nil
		})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("TTL %s: %v", ttl, err)
		}
	}
}

func TestLifecycleLockRenewalRespectsRemainingLease(t *testing.T) {
	for _, delay := range []time.Duration{5 * time.Second, 15 * time.Second} {
		t.Run(delay.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				calls := 0
				base := &lifecycleLock{acquired: true}
				lock := &renewalLock{workflowLockStore: delayedAcquireLock{workflowLockStore: base, delay: delay}, extend: func(ctx context.Context, _, _ string, _ time.Duration) (bool, error) {
					calls++
					deadline, ok := ctx.Deadline()
					if !ok || !deadline.Equal(start.Add(20*time.Second)) {
						t.Errorf("remaining-lease deadline = %v", deadline)
					}
					<-ctx.Done()
					return false, ctx.Err()
				}}
				err := (&Repository{rdb: lock}).withLifecycleLock(t.Context(), "workflow:BUILD:one", 30*time.Second, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
				if status.Code(err) != codes.Unavailable || time.Since(start) >= 30*time.Second {
					t.Fatalf("lease budget failure = %v after %s", err, time.Since(start))
				}
				if (delay == 5*time.Second && calls != 1) || (delay == 15*time.Second && calls != 0) {
					t.Fatalf("renewals outside safe budget: %d", calls)
				}
				assertLockReleased(t, base, 1)
			})
		})
	}
}

func TestLifecycleLockParentCancellationRemainsRetryable(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprintf("deadline=%v", deadline), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				err := (&Repository{rdb: &lifecycleLock{acquired: true}}).withLifecycleLock(ctx, "workflow:BUILD:one", time.Minute, func(ctx context.Context) error {
					if !deadline {
						cancel()
					}
					<-ctx.Done()
					return ctx.Err()
				})
				want := codes.Canceled
				if deadline {
					want = codes.DeadlineExceeded
				}
				if status.Code(err) != want || kafkapkg.ShouldCommitOnError(err) {
					t.Fatalf("parent cancellation = %v; commit=%v", err, kafkapkg.ShouldCommitOnError(err))
				}
			})
		})
	}
}
