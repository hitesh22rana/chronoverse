//nolint:testpackage // Tests executor helper contracts and dependency boundaries.
package executor

import (
	"context"
	"math"
	"runtime"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

func TestRecoveryNumericAndMetadataContracts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		input int
		want  int32
	}{{-1, 0}, {0, 0}, {1, 1}, {math.MaxInt32, math.MaxInt32}, {math.MaxInt32 + 1, math.MaxInt32}} {
		if got := boundedInt32(tc.input); got != tc.want {
			t.Errorf("boundedInt32(%d)=%d, want %d", tc.input, got, tc.want)
		}
	}
	for _, tc := range []struct {
		batch       int32
		concurrency int
		want        int32
	}{{0, 0, 1}, {-1, 5, 1}, {5, 0, 5}, {5, 9, 5}, {5, 2, 2}} {
		repo := &Repository{cfg: Config{RecoveryBatchSize: tc.batch, Concurrency: tc.concurrency}}
		if got := repo.recoveryClaimLimit(); got != tc.want {
			t.Errorf("claim limit(%d,%d)=%d, want %d", tc.batch, tc.concurrency, got, tc.want)
		}
	}
	if workflowFromExpiredLease(nil) != nil {
		t.Fatal("absent lease produced workflow metadata")
	}
	job := &jobspb.ExpiredJobLease{WorkflowId: "workflow", UserId: "owner", LogRetention: true}
	workflow := workflowFromExpiredLease(job)
	if workflow.GetId() != "workflow" || workflow.GetUserId() != "owner" || !workflow.GetLogRetention() {
		t.Fatalf("metadata=%v", workflow)
	}
	cfg := Config{}
	normalizeConcurrency(&cfg)
	if cfg.Concurrency != max(1, runtime.GOMAXPROCS(0)) || cfg.AwaitingReconciliationLimit != cfg.Concurrency {
		t.Fatalf("normalized concurrency=%+v", cfg)
	}
	cfg = Config{LeaseDuration: time.Nanosecond, LeaseRenewInterval: time.Nanosecond}
	normalizeLeaseIntervals(&cfg)
	if cfg.LeaseRenewInterval != time.Second {
		t.Fatalf("subnanosecond division fallback=%v", cfg.LeaseRenewInterval)
	}
	repo := &Repository{}
	repo.recoverClaimedExpiredLeases(t.Context(), []*jobspb.ExpiredJobLease{nil})
	if got := classifyExecutionFailure(nil); got.Retryable || got.ReasonCode != "EXECUTION_FAILED" {
		t.Fatalf("nil failure=%+v", got)
	}
}

func TestContainerEndpointRejectsMissingConfiguration(t *testing.T) {
	t.Parallel()
	repo := &Repository{svc: &Services{}}
	for _, tc := range []struct {
		endpoint string
		code     codes.Code
	}{{"", codes.Unavailable}, {"tcp://runtime:2375", codes.FailedPrecondition}} {
		svc, err := repo.containerSvcForEndpoint("runtime", tc.endpoint)
		if svc != nil || status.Code(err) != tc.code {
			t.Fatalf("endpoint %q=(%v,%v)", tc.endpoint, svc, err)
		}
	}
	if err := repo.cleanupContainer(t.Context(), nil, "container"); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("cleanup=%v", err)
	}
}

type boundaryReconciliationClient struct {
	jobspb.JobsServiceClient
	calls int
	err   error
}

func (c *boundaryReconciliationClient) ClaimJob(_ context.Context, _ *jobspb.ClaimJobRequest, _ ...grpc.CallOption) (*jobspb.ClaimJobResponse, error) {
	c.calls++
	return &jobspb.ClaimJobResponse{Claimed: true}, c.err
}

func TestReconciliationOnlyReleasesProvenMissingAuthority(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"canceled", "authorization", "unavailable", "missing"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			gate := newHandoffRegistry(1)
			entry, _, err := gate.getOrReserve("command", &jobspb.ClaimJobRequest{Id: "job", CommandId: "command"})
			if err != nil {
				t.Fatal(err)
			}
			if !gate.activate(entry, &jobspb.ClaimJobResponse{Id: "job", Claimed: true, LeaseToken: "lease"}) {
				t.Fatal("activation failed")
			}
			gate.markAwaiting("command")
			client := &boundaryReconciliationClient{err: status.Error(codes.Unavailable, "database unavailable")}
			repo := &Repository{handoffs: gate, auth: fakeAuth{}, svc: &Services{Jobs: client}}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			wantCalls, wantSize := 1, 1
			switch name {
			case "canceled":
				cancel()
				wantCalls = 0
			case "authorization":
				repo.auth = failingTokenAuth{}
				wantCalls = 0
			case "missing":
				client.err = status.Error(codes.NotFound, "deleted")
				wantSize = 0
			}
			repo.reconcileHandoffs(ctx)
			if client.calls != wantCalls || gate.size() != wantSize {
				t.Fatalf("calls/retained=%d/%d, want %d/%d", client.calls, gate.size(), wantCalls, wantSize)
			}
		})
	}
}

func TestJobLogDiagnosticFieldsAcceptMissingAndMalformedRecords(t *testing.T) {
	t.Parallel()
	for _, records := range [][]*kgo.Record{nil, {nil}} {
		if fields := jobLogBatchFields(records); len(fields) != 0 {
			t.Fatalf("absent record fields=%v", fields)
		}
	}
	fields := jobLogBatchFields([]*kgo.Record{{Topic: "logs", Key: []byte("job"), Value: []byte("invalid json")}})
	if len(fields) != 2 || fields[0].String != "logs" || fields[1].String != "job" {
		t.Fatalf("malformed record identity=%v", fields)
	}
	repo := &Repository{}
	repo.publishJobLogBatch(t.Context(), nil)
	if done := repo.runLiveJobLogPublisher(t.Context(), nil); done != nil {
		t.Fatal("unconfigured publisher started")
	}
	if err := repo.executeHeartbeatWorkflow(t.Context(), &workflowspb.GetWorkflowByIDResponse{Payload: "{"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid heartbeat payload=%v", err)
	}
}

func TestRemovedHandoffOwnerCannotResolveReplacement(t *testing.T) {
	t.Parallel()
	gate := newHandoffRegistry(1)
	request := &jobspb.ClaimJobRequest{Id: "job", CommandId: "command"}
	old, _, err := gate.getOrReserve("command", request)
	if err != nil {
		t.Fatal(err)
	}
	gate.resolveRemoved(old, status.Error(codes.Unavailable, "claim failed"))
	replacement, owner, err := gate.getOrReserve("command", request)
	if err != nil || !owner {
		t.Fatalf("replacement reservation=(%v,%v)", owner, err)
	}
	gate.resolveRemoved(old, nil)
	if gate.size() != 1 {
		t.Fatal("stale owner removed replacement")
	}
	select {
	case <-replacement.done:
		t.Fatal("stale owner resolved replacement waiters")
	default:
	}
	if !gate.activate(replacement, &jobspb.ClaimJobResponse{Id: "job", Claimed: true, LeaseToken: "lease"}) {
		t.Fatal("replacement activation failed")
	}
	gate.resolveRemoved(replacement, status.Error(codes.Unavailable, "late failure"))
	if gate.size() != 1 || replacement.result != nil {
		t.Fatal("late claiming failure changed active authority")
	}
}
