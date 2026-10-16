//nolint:testpackage // Exercises private worker handoff contracts.
package executor

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

type dispatchJobsClient struct {
	jobspb.JobsServiceClient
	claim func(context.Context, *jobspb.ClaimJobRequest) (*jobspb.ClaimJobResponse, error)
	renew func(context.Context, *jobspb.RenewJobLeaseRequest) (*jobspb.RenewJobLeaseResponse, error)
}

func (c *dispatchJobsClient) ClaimJob(ctx context.Context, req *jobspb.ClaimJobRequest, _ ...grpc.CallOption) (*jobspb.ClaimJobResponse, error) {
	return c.claim(ctx, req)
}

func dispatchRepository(t *testing.T) (*Repository, *kgo.Record) {
	t.Helper()
	payload, err := json.Marshal(&jobsmodel.ScheduledJobEntry{JobID: "job-1", WorkflowID: "workflow-1", ScheduledAt: time.Now().UTC().Format(time.RFC3339Nano), DispatchAttempt: 3, WorkflowGeneration: 7})
	if err != nil {
		t.Fatal(err)
	}
	repo := &Repository{tp: otel.Tracer("executor-dispatch-test"), auth: fakeAuth{}, cfg: defaultConfig(), slots: make(chan struct{}, 1), processID: "process-1", svc: &Services{}}
	repo.cfg.Concurrency = 1
	return repo, &kgo.Record{Topic: "jobs", Partition: 2, Offset: 15, Key: []byte("workflow-1"), Value: payload}
}

func TestExecutorProcessRecordReleasesRejectedWork(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		failure error
	}{
		{name: "claim declined"},
		{name: "retryable claim failure", failure: status.Error(codes.Unavailable, "retry claim")},
		{name: "terminal claim failure", failure: status.Error(codes.InvalidArgument, "bad claim")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo, record := dispatchRepository(t)
			calls := 0
			repo.svc.Jobs = &dispatchJobsClient{claim: func(_ context.Context, req *jobspb.ClaimJobRequest) (*jobspb.ClaimJobResponse, error) {
				calls++
				if req.GetId() != "job-1" || req.GetWorkflowId() != "workflow-1" ||
					req.GetDispatchAttempt() != 3 || req.GetWorkerId() != repo.cfg.WorkerID ||
					req.GetProcessInstanceId() != "process-1" || req.GetLeaseDurationSeconds() != 30 ||
					req.GetCommandId() != idempotency.ClaimCommandID("process-1", "job-1", 3) {
					t.Errorf("claim identity = %v", req)
				}
				return &jobspb.ClaimJobResponse{Claimed: false, Reason: "already claimed"}, tc.failure
			}}
			for range 2 {
				err := repo.processRecord(t.Context(), record)
				if !errors.Is(err, tc.failure) {
					t.Fatalf("processRecord error = %v, want %v", err, tc.failure)
				}
				if len(repo.slots) != 0 || repo.handoffs.size() != 0 {
					t.Fatalf("rejected work leaked slots=%d handoffs=%d", len(repo.slots), repo.handoffs.size())
				}
			}
			if calls != 2 {
				t.Fatalf("claim calls = %d, want replay retry", calls)
			}
		})
	}
}

func TestExecutorProcessRecordRejectsBeforeClaim(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"malformed record", "invalid attempt", "invalid timestamp", "authorization failure", "canceled slot wait", "handoff capacity"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repo, record := dispatchRepository(t)
			repo.svc.Jobs = &dispatchJobsClient{claim: func(context.Context, *jobspb.ClaimJobRequest) (*jobspb.ClaimJobResponse, error) {
				t.Error("unexpected ClaimJob")
				return nil, status.Error(codes.Internal, "unexpected claim")
			}}
			ctx := t.Context()
			var want error
			wantCode := codes.OK
			wantSlots := 0
			wantHandoffs := 0
			switch name {
			case "malformed record":
				record.Value = []byte("{")
				wantCode = codes.InvalidArgument
			case "invalid attempt":
				record.Value = []byte(`{"DispatchAttempt":0}`)
				wantCode = codes.InvalidArgument
			case "invalid timestamp":
				record.Value = []byte(`{"DispatchAttempt":1,"ScheduledAt":"yesterday"}`)
				wantCode = codes.InvalidArgument
			case "authorization failure":
				repo.auth = failingTokenAuth{}
				wantCode = codes.Unavailable
			case "canceled slot wait":
				repo.slots <- struct{}{}
				wantSlots = 1
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
				want = context.Canceled
			case "handoff capacity":
				repo.handoffs = newHandoffRegistry(1)
				if _, owner, err := repo.handoffs.getOrReserve("other", &jobspb.ClaimJobRequest{Id: "other"}); err != nil || !owner {
					t.Fatalf("reserve = %t, %v", owner, err)
				}
				wantCode = codes.ResourceExhausted
				wantHandoffs = 1
			}
			err := repo.processRecord(ctx, record)
			if want != nil {
				if !errors.Is(err, want) {
					t.Fatalf("error = %v, want %v", err, want)
				}
			} else if status.Code(err) != wantCode {
				t.Fatalf("error = %v, want %s", err, wantCode)
			}
			if len(repo.slots) != wantSlots || repo.handoffs.size() != wantHandoffs {
				t.Fatalf("slots=%d handoffs=%d, want %d/%d", len(repo.slots), repo.handoffs.size(), wantSlots, wantHandoffs)
			}
		})
	}
}

func TestExecutorProcessRecordFailsClosedWhenPlaceholderVanishes(t *testing.T) {
	t.Parallel()
	repo, record := dispatchRepository(t)
	commandID := idempotency.ClaimCommandID(repo.processID, "job-1", 3)
	repo.svc.Jobs = &dispatchJobsClient{claim: func(context.Context, *jobspb.ClaimJobRequest) (*jobspb.ClaimJobResponse, error) {
		// Simulate the claiming placeholder being resolved while ClaimJob is in
		// flight, so this caller survives the claim without ownership to activate.
		entry, owner, err := repo.handoffs.getOrReserve(commandID, &jobspb.ClaimJobRequest{})
		if err != nil || owner {
			t.Errorf("placeholder lookup = owner %t, err %v", owner, err)
		}
		repo.handoffs.resolveRemoved(entry, nil)
		return &jobspb.ClaimJobResponse{Claimed: true, Id: "job-1", WorkflowId: "workflow-1", LeaseToken: "lease-1"}, nil
	}}
	err := repo.processRecord(t.Context(), record)
	if status.Code(err) != codes.Internal {
		t.Fatalf("processRecord error = %v, want Internal", err)
	}
	if len(repo.slots) != 0 || repo.handoffs.size() != 0 {
		t.Fatalf("failed activation leaked slots=%d handoffs=%d", len(repo.slots), repo.handoffs.size())
	}
}

func TestExecutorDuplicateRecordWaitsForClaimOwner(t *testing.T) {
	t.Parallel()
	repo, record := dispatchRepository(t)
	repo.slots = make(chan struct{}, 2)
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	failure := status.Error(codes.Unavailable, "claim unavailable")
	repo.svc.Jobs = &dispatchJobsClient{claim: func(ctx context.Context, _ *jobspb.ClaimJobRequest) (*jobspb.ClaimJobResponse, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			return nil, failure
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ownerDone := make(chan error, 1)
	go func() { ownerDone <- repo.processRecord(ctx, record) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("owner did not enter ClaimJob")
	}
	waiterCtx, cancelWaiter := context.WithCancel(ctx)
	cancelWaiter()
	// The owner occupies one slot; the duplicate must leave its permit behind.
	err := repo.processRecord(waiterCtx, record)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("duplicate error = %v", err)
	}
	if calls.Load() != 1 || repo.handoffs.size() != 1 || len(repo.slots) != 1 {
		t.Fatalf("duplicate disturbed owner: calls=%d handoffs=%d slots=%d", calls.Load(), repo.handoffs.size(), len(repo.slots))
	}
	close(release)
	select {
	case err = <-ownerDone:
		if !errors.Is(err, failure) {
			t.Fatalf("owner error = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("owner did not finish")
	}
	if repo.handoffs.size() != 0 || len(repo.slots) != 0 {
		t.Fatal("owner leaked capacity")
	}
}

func (c *dispatchJobsClient) RenewJobLease(ctx context.Context, req *jobspb.RenewJobLeaseRequest, _ ...grpc.CallOption) (*jobspb.RenewJobLeaseResponse, error) {
	return c.renew(ctx, req)
}

func TestExecutorActivatedHandoffSurvivesRecordCancellation(t *testing.T) {
	t.Parallel()
	repo, record := dispatchRepository(t)
	repo.slots = make(chan struct{}, 2)
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	recordCtx, cancelRecord := context.WithCancel(ctx)
	defer cancelRecord()
	repo.svc.Jobs = &dispatchJobsClient{
		claim: func(context.Context, *jobspb.ClaimJobRequest) (*jobspb.ClaimJobResponse, error) {
			calls.Add(1)
			return &jobspb.ClaimJobResponse{Claimed: true, Id: "job-1", WorkflowId: "workflow-1", LeaseToken: "lease-1"}, nil
		},
		renew: func(runCtx context.Context, req *jobspb.RenewJobLeaseRequest) (*jobspb.RenewJobLeaseResponse, error) {
			if req.GetId() != "job-1" || req.GetLeaseToken() != "lease-1" {
				t.Errorf("lease identity = %v", req)
			}
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if runCtx.Err() != nil {
				t.Errorf("execution inherited record cancellation: %v", runCtx.Err())
			}
			return nil, status.Error(codes.Unavailable, "lease temporarily unavailable")
		},
	}
	t.Cleanup(func() {
		close(release)
		repo.execWG.Wait()
		if len(repo.slots) != 0 || len(repo.handoffs.awaiting()) != 1 {
			t.Errorf("finished execution slots=%d awaiting=%d", len(repo.slots), len(repo.handoffs.awaiting()))
		}
	})
	if err := repo.processRecord(recordCtx, record); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("execution did not renew lease")
	}
	cancelRecord()
	if len(repo.slots) != 1 || repo.handoffs.size() != 1 {
		t.Fatal("active execution lost capacity ownership")
	}
	duplicateCtx, cancelDuplicate := context.WithTimeout(ctx, time.Second)
	defer cancelDuplicate()
	if err := repo.processRecord(duplicateCtx, record); err != nil {
		t.Fatalf("duplicate = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("duplicate claims = %d", calls.Load())
	}
}
