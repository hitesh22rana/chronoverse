//nolint:testpackage // Verifies durable report and cancellation boundaries of discovery.
package executor

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	containerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

type discoveryFaultJobs struct {
	*scriptedJobsClient
	claim                 func(context.Context) (*jobspb.ListContainerIntentsResponse, error)
	observeErr, reportErr error
	reports               []*jobspb.ContainerIntentCommand
	observations          int
	quarantine            bool
}

func (j *discoveryFaultJobs) ClaimContainerIntents(ctx context.Context, _ *jobspb.ContainerIntentCommand, _ ...grpc.CallOption) (*jobspb.ListContainerIntentsResponse, error) {
	return j.claim(ctx)
}

func (j *discoveryFaultJobs) ObserveContainer(_ context.Context, r *jobspb.ContainerIntentCommand, _ ...grpc.CallOption) (*jobspb.ContainerIntentResponse, error) {
	j.observations++
	return fakeIntent(r), j.observeErr
}

func (j *discoveryFaultJobs) ReportContainerInspection(_ context.Context, r *jobspb.ContainerIntentCommand, _ ...grpc.CallOption) (*jobspb.ContainerIntentResponse, error) {
	j.reports = append(j.reports, r)
	return &jobspb.ContainerIntentResponse{Intent: &jobspb.ContainerIntent{Quarantined: j.quarantine}}, j.reportErr
}

type erroneousSuccessError struct{}

func (erroneousSuccessError) Error() string { return "runtime returned an error with success status" }

func (erroneousSuccessError) GRPCStatus() *status.Status {
	return status.New(codes.OK, "invalid runtime status")
}

func TestDiscoveryClaimFailureAndShutdownDoNotContactRuntime(t *testing.T) {
	for _, scenario := range []string{"authorization", "storage", "empty", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			auth := &intentFaultAuth{}
			if scenario == "authorization" {
				auth.failAt = 1
			}
			f := newClaimedRunFixtureWithAuth(&scriptedJobsClient{}, &scriptedWorkflowsClient{}, nil, nil, auth)
			calls := 0
			jobs := &discoveryFaultJobs{scriptedJobsClient: &scriptedJobsClient{}, claim: func(context.Context) (*jobspb.ListContainerIntentsResponse, error) {
				calls++
				if scenario == "storage" {
					return nil, status.Error(codes.Unavailable, "claim unavailable")
				}
				return &jobspb.ListContainerIntentsResponse{}, nil
			}}
			f.repo.svc.Jobs = jobs
			f.repo.svc.CsvcForEndpoint = func(string, string) (ContainerSvc, error) {
				t.Fatal("failed admission contacted a runtime")
				return nil, status.Error(codes.Internal, "unexpected runtime lookup")
			}
			f.repo.cfg.RecoveryInterval = time.Second
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if scenario == "canceled" {
				cancel()
				f.repo.reconcileContainerIntents(ctx)
			} else {
				f.repo.reconcileContainerIntentBatch(ctx)
			}
			if scenario == "authorization" || scenario == "canceled" {
				if calls != 0 {
					t.Fatal("unauthorized/canceled discovery reached storage")
				}
			} else if calls != 1 {
				t.Fatalf("failed claim was retried without backoff: calls=%d", calls)
			}
		})
	}
}

//nolint:gocyclo // One inspection matrix checks durable reporting and never-start authority across independent failures.
func TestDiscoveryReportsObservationAndKeepsFailuresDurable(t *testing.T) {
	for _, scenario := range []string{"observed", "observation authorization", "observation storage", "report authorization", "report storage", "quarantine", "invalid success status"} {
		t.Run(scenario, func(t *testing.T) {
			auth := &intentFaultAuth{}
			if scenario == "observation authorization" {
				auth.failAt = 1
			}
			if scenario == "report authorization" {
				auth.failAt = 2
			}
			runtime := &intentFaultRuntime{inspectFn: func(int, string) (*containerpkg.State, error) {
				return &containerpkg.State{ID: "immutable-container", Labels: map[string]string{"chronoverse.intent_id": "intent", "chronoverse.job_id": "job", "chronoverse.attempt": "1"}}, nil
			}}
			f := newClaimedRunFixtureWithAuth(&scriptedJobsClient{}, &scriptedWorkflowsClient{}, nil, runtime, auth)
			jobs := &discoveryFaultJobs{scriptedJobsClient: &scriptedJobsClient{}, quarantine: scenario == "quarantine"}
			if scenario == "observation storage" {
				jobs.observeErr = status.Error(codes.Aborted, "inspection lease lost")
			}
			if scenario == "report storage" {
				jobs.reportErr = status.Error(codes.Unavailable, "report unavailable")
			}
			f.repo.svc.Jobs = jobs
			if scenario == "invalid success status" {
				f.repo.svc.CsvcForEndpoint = func(string, string) (ContainerSvc, error) { return nil, erroneousSuccessError{} }
			}
			intent := &jobspb.ContainerIntent{
				Id:              "intent",
				JobId:           "job",
				Name:            "cv-intent",
				Attempt:         1,
				Generation:      2,
				Token:           "inspection-token",
				RuntimeInstance: "test-daemon",
				RuntimeNodeId:   "runtime-1",
				RuntimeEndpoint: "tcp://docker-proxy:2375",
			}
			f.repo.inspectContainerIntent(t.Context(), intent)
			if scenario == "report authorization" {
				if len(jobs.reports) != 0 {
					t.Fatal("unauthorized report reached storage")
				}
				return
			}
			if len(jobs.reports) != 1 {
				t.Fatalf("missing durable report: %d", len(jobs.reports))
			}
			report := jobs.reports[0]
			if report.GetIntentId() != intent.GetId() || report.GetToken() != intent.GetToken() || report.GetGeneration() != 2 {
				t.Fatalf("report lost inspection fence: %v", report)
			}
			want := "OBSERVED"
			if scenario == "observation authorization" || scenario == "observation storage" || scenario == "invalid success status" {
				want = "ERROR"
			}
			if report.GetOutcome() != want {
				t.Fatalf("report falsely acknowledged observation: %v", report)
			}
			if scenario == "invalid success status" && report.GetErrorCode() != codes.Unavailable.String() {
				t.Fatalf("non-nil runtime error reported as success: %v", report)
			}
			if runtime.startCalls != 0 {
				t.Fatal("discovery started a container")
			}
		})
	}
}
