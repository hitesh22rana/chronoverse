//nolint:testpackage // Uses the execution and recovery fixtures at their owning boundaries.
package executor

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

func TestStalledLeaseRenewalCancelsWorkBeforeCallerDeadline(t *testing.T) {
	for _, recovered := range []bool{false, true} {
		name := "execution"
		if recovered {
			name = "recovery"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var calls int
				heartbeat := newBlockingHeartbeatSvc()
				jobs := &scriptedJobsClient{renewLease: func(ctx context.Context, _ *jobspb.RenewJobLeaseRequest) error {
					calls++
					if calls == 1 {
						return nil
					}
					<-ctx.Done()
					return ctx.Err()
				}}
				fixture := newClaimedRunFixture(jobs, &scriptedWorkflowsClient{flow: heartbeatWorkflow()}, heartbeat, nil)
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				claim := claimedJob(1)
				claim.Trigger = "MANUAL"
				var err, cause error
				if recovered {
					err = fixture.repo.withRecoveredLeaseRenewal(ctx, claim, func(workCtx context.Context) error {
						<-workCtx.Done()
						cause = context.Cause(workCtx)
						return workCtx.Err()
					})
				} else {
					err = fixture.repo.runClaimedWorkflow(ctx, claim, time.Now(), 0)
				}
				if ctx.Err() != nil {
					t.Fatalf("work survived stalled renewal until caller deadline: %v", err)
				}
				if recovered && status.Code(cause) != codes.DeadlineExceeded {
					t.Fatalf("renewal cancellation cause=%v", cause)
				}
				if calls != 2 {
					t.Fatalf("renewal calls=%d, want initial proof and stalled extension", calls)
				}
			})
		})
	}
}

func TestExecutionJoinsRenewalBeforeReturning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{})
		var calls int
		var stopped atomic.Bool
		jobs := &scriptedJobsClient{
			renewLease: func(ctx context.Context, _ *jobspb.RenewJobLeaseRequest) error {
				calls++
				if calls == 1 {
					return nil
				}
				close(entered)
				<-ctx.Done()
				time.Sleep(time.Second)
				stopped.Store(true)
				return ctx.Err()
			},
			scheduleJob: func(context.Context, *jobspb.ScheduleJobRequest) error {
				<-entered
				return status.Error(codes.Unavailable, "scheduler unavailable")
			},
		}
		fixture := newClaimedRunFixture(jobs, &scriptedWorkflowsClient{flow: heartbeatWorkflow()}, nil, nil)
		if err := fixture.repo.runClaimedWorkflow(t.Context(), claimedJob(1), time.Now(), 0); err != nil {
			t.Fatal(err)
		}
		joined := stopped.Load()
		time.Sleep(2 * time.Second)
		if !joined {
			t.Fatal("execution returned while its lease renewal was still running")
		}
	})
}

func TestInitialLeaseProofHasBoundedDeadline(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		duration, interval, callerBudget time.Duration
	}{
		{name: "default lease", duration: 30 * time.Second, interval: 10 * time.Second, callerBudget: time.Minute},
		{name: "late renewal interval", duration: 3 * time.Second, interval: 2700 * time.Millisecond, callerBudget: time.Minute},
		{name: "earlier caller deadline", duration: 30 * time.Second, interval: 10 * time.Second, callerBudget: 20 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				jobs := &scriptedJobsClient{renewLease: func(ctx context.Context, _ *jobspb.RenewJobLeaseRequest) error {
					<-ctx.Done()
					return nil // A late response must not admit work on an unconfirmed lease.
				}}
				fixture := newClaimedRunFixture(jobs, &scriptedWorkflowsClient{flow: heartbeatWorkflow()}, nil, nil)
				fixture.repo.cfg.LeaseDuration = tc.duration
				fixture.repo.cfg.LeaseRenewInterval = tc.interval
				ctx, cancel := context.WithTimeout(t.Context(), tc.callerBudget)
				defer cancel()
				started := time.Now()
				err := fixture.repo.runClaimedWorkflow(ctx, claimedJob(1), started, 0)
				if status.Code(err) != codes.DeadlineExceeded || time.Since(started) > min(5*time.Second, tc.callerBudget) || time.Since(started) >= tc.duration-tc.interval {
					t.Fatalf("initial proof elapsed=%v err=%v", time.Since(started), err)
				}
				if fixture.lookups.count() != 0 || len(settleOps(fixture.log.snapshot())) != 0 {
					t.Fatal("unconfirmed lease admitted workflow access or settlement")
				}
			})
		})
	}
}

func TestInvalidLeaseWindowDoesNotAdmitExecution(t *testing.T) {
	fixture := newClaimedRunFixture(&scriptedJobsClient{}, &scriptedWorkflowsClient{flow: heartbeatWorkflow()}, nil, nil)
	fixture.repo.cfg.LeaseRenewInterval = fixture.repo.cfg.LeaseDuration
	err := fixture.repo.runClaimedWorkflow(t.Context(), claimedJob(1), time.Now(), 0)
	if status.Code(err) != codes.InvalidArgument || len(fixture.log.snapshot()) != 0 || fixture.lookups.count() != 0 {
		t.Fatalf("invalid lease window admitted work: %v", err)
	}
}
