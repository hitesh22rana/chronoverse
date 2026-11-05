//nolint:testpackage // Asserts recovery batching through the jobs RPC boundary.
package executor

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

const recoveryContractProcessID = "00000000-0000-4000-8000-000000000010"

type recoveryBudgetClient struct {
	jobspb.JobsServiceClient
	t        *testing.T
	batches  []int32
	listErr  error
	claimed  int
	mu       sync.Mutex
	renewals map[string]int
	releases map[string]*jobspb.ReleaseJobForRetryRequest
}

func (c *recoveryBudgetClient) RecoverExpiredJobLeases(_ context.Context, req *jobspb.RecoverExpiredJobLeasesRequest, _ ...grpc.CallOption) (*jobspb.RecoverExpiredJobLeasesResponse, error) {
	if req.GetWorkerId() != "execution-worker" || req.GetProcessInstanceId() != recoveryContractProcessID || req.GetLeaseDurationSeconds() != 30 || req.GetCommandId() == "" {
		c.t.Errorf("invalid recovery ownership request: %v", req)
	}
	c.batches = append(c.batches, req.GetBatchSize())
	if c.listErr != nil {
		return nil, c.listErr
	}
	response := &jobspb.RecoverExpiredJobLeasesResponse{}
	for range req.GetBatchSize() {
		c.claimed++
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", c.claimed)
		response.Jobs = append(response.Jobs, &jobspb.ExpiredJobLease{
			Id: id, WorkflowId: "00000000-0000-4000-8000-000000000020", UserId: "00000000-0000-4000-8000-000000000030", LeaseToken: "execution-worker:" + id,
			Attempts: 1, RuntimeUnavailable: true,
		})
	}
	return response, nil
}

func (c *recoveryBudgetClient) RenewJobLease(_ context.Context, req *jobspb.RenewJobLeaseRequest, _ ...grpc.CallOption) (*jobspb.RenewJobLeaseResponse, error) {
	if req.GetLeaseToken() != "execution-worker:"+req.GetId() || req.GetLeaseDurationSeconds() != 30 {
		c.t.Errorf("renewed wrong ownership: %v", req)
	}
	c.mu.Lock()
	c.renewals[req.GetId()]++
	c.mu.Unlock()
	return &jobspb.RenewJobLeaseResponse{}, nil
}

func (c *recoveryBudgetClient) ReleaseJobForRetry(_ context.Context, req *jobspb.ReleaseJobForRetryRequest, _ ...grpc.CallOption) (*jobspb.ReleaseJobForRetryResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.releases[req.GetId()]; exists {
		c.t.Errorf("duplicate settlement for %s", req.GetId())
	}
	c.releases[req.GetId()] = req
	return &jobspb.ReleaseJobForRetryResponse{}, nil
}

func TestRecoveryBudgetShrinksFinalWaveAndSettlesEveryLease(t *testing.T) {
	t.Parallel()
	client := &recoveryBudgetClient{t: t, renewals: make(map[string]int), releases: make(map[string]*jobspb.ReleaseJobForRetryRequest)}
	cfg := defaultConfig()
	cfg.RecoveryBatchSize, cfg.Concurrency = 5, 2
	repo := &Repository{cfg: cfg, auth: fakeAuth{}, processID: recoveryContractProcessID, svc: &Services{Jobs: client}}
	repo.ensureHandoffState()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	before := time.Now()
	repo.recoverExpiredLeaseBatch(ctx)
	if ctx.Err() != nil {
		t.Fatalf("recovery did not finish: %v", ctx.Err())
	}
	if !reflect.DeepEqual(client.batches, []int32{2, 2, 1}) {
		t.Fatalf("recovery batches = %v, want bounded waves 2,2,1", client.batches)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.renewals) != 5 || len(client.releases) != 5 {
		t.Fatalf("renewed/released = %d/%d, want 5/5", len(client.renewals), len(client.releases))
	}
	commands := make(map[string]bool)
	for id, request := range client.releases {
		next, err := time.Parse(time.RFC3339Nano, request.GetNextAttemptAt())
		if err != nil || next.Before(before) {
			t.Errorf("invalid retry instant %q: %v", request.GetNextAttemptAt(), err)
		}
		if client.renewals[id] != 1 || request.GetLeaseToken() != "execution-worker:"+id || request.GetErrorCode() != codes.Unavailable.String() || request.GetCommandId() == "" {
			t.Errorf("incorrect lease settlement: renewals=%d request=%v", client.renewals[id], request)
		}
		if commands[request.GetCommandId()] {
			t.Error("different leases shared a settlement command identity")
		}
		commands[request.GetCommandId()] = true
	}
}

func TestLeaseRenewalConfigurationRetainsAWindowBeforeExpiry(t *testing.T) {
	t.Parallel()
	for _, interval := range []time.Duration{time.Second, 9 * time.Second, 12 * time.Second} {
		t.Run(interval.String(), func(t *testing.T) {
			t.Parallel()
			cfg, err := normalizeConfig(&Config{LeaseDuration: 9 * time.Second, LeaseRenewInterval: interval})
			if err != nil {
				t.Fatal(err)
			}
			want := interval
			if interval >= 9*time.Second {
				want = 3 * time.Second
			}
			if cfg.LeaseRenewInterval != want || cfg.LeaseRenewInterval >= cfg.LeaseDuration {
				t.Fatalf("renewal/lease interval = %v/%v, want %v before expiry", cfg.LeaseRenewInterval, cfg.LeaseDuration, want)
			}
		})
	}
}

func TestRecoveryAcquisitionFailureDoesNotSettleAnyLease(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"canceled caller", "authorization rejected", "jobs unavailable"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client := &recoveryBudgetClient{t: t, renewals: make(map[string]int), releases: make(map[string]*jobspb.ReleaseJobForRetryRequest)}
			repo := &Repository{cfg: defaultConfig(), auth: fakeAuth{}, processID: recoveryContractProcessID, svc: &Services{Jobs: client}}
			repo.ensureHandoffState()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			wantCalls := 0
			switch name {
			case "canceled caller":
				cancel()
			case "authorization rejected":
				repo.auth = failingTokenAuth{}
			case "jobs unavailable":
				client.listErr = status.Error(codes.Unavailable, "jobs database unavailable")
				wantCalls = 1
			}
			repo.recoverExpiredLeaseBatch(ctx)
			if len(client.batches) != wantCalls || client.claimed != 0 || len(client.renewals) != 0 || len(client.releases) != 0 {
				t.Fatalf("acquisition failure caused work: requests=%v claimed=%d renewals=%d releases=%d", client.batches, client.claimed, len(client.renewals), len(client.releases))
			}
			if repo.handoffs.size() != 0 {
				t.Fatal("failed acquisition retained handoff ownership")
			}
		})
	}
}
