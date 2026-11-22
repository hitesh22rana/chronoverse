//nolint:testpackage // Cleanup authority is checked at the worker/runtime boundary.
package workflow

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	kafkapkg "github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

type cleanupProvenanceJobs struct {
	*testJobsClient
	lookup func(*jobspb.ContainerIntentCommand) (*jobspb.ContainerIntentResponse, error)
}

func (j *cleanupProvenanceJobs) GetContainerIntent(
	_ context.Context,
	req *jobspb.ContainerIntentCommand,
	_ ...grpc.CallOption,
) (
	*jobspb.ContainerIntentResponse,
	error,
) {
	return j.lookup(req)
}

type cleanupProvenanceRuntime struct {
	durableCleanupContainer
	identities int
	faultAt    int
	mismatch   bool
}

func (c *cleanupProvenanceRuntime) RuntimeInstance(context.Context) (string, error) {
	c.identities++
	if c.identities == c.faultAt {
		if c.mismatch {
			return "replacement-daemon", nil
		}
		return "", status.Error(codes.Unavailable, "identity endpoint unavailable")
	}
	return "original-daemon", nil
}

//nolint:gocyclo // One cleanup boundary matrix checks side effects and proof before and after daemon repoint.
func TestCleanupRejectsUnprovenRoutingAndDaemonIdentity(t *testing.T) {
	for _, scenario := range []string{
		"lookup failure",
		"routing mismatch",
		"identity read before stop",
		"repoint before stop",
		"identity read after removal",
		"repoint after removal",
		"verified lookup",
	} {
		t.Run(scenario, func(t *testing.T) {
			stopped, removed, receipts, factories := 0, 0, 0, 0
			jobs := &cleanupProvenanceJobs{testJobsClient: &testJobsClient{recordReceipt: func(_ context.Context, req *jobspb.ContainerCleanupReceiptRequest) error {
				receipts++
				if req.GetRuntimeInstance() != "original-daemon" || req.GetIntentId() != "intent-original" {
					t.Errorf("receipt retargeted: %v", req)
				}
				return nil
			}}}
			jobs.lookup = func(req *jobspb.ContainerIntentCommand) (*jobspb.ContainerIntentResponse, error) {
				if req.GetJobId() != "job-original" || req.GetContainerId() != "container-original" {
					t.Errorf("lookup retargeted: %v", req)
				}
				if scenario == "lookup failure" {
					return nil, status.Error(codes.Unavailable, "provenance unavailable")
				}
				endpoint := "tcp://original:2376"
				if scenario == "routing mismatch" {
					endpoint = "tcp://replacement:2376"
				}
				return &jobspb.ContainerIntentResponse{Intent: &jobspb.ContainerIntent{Id: "intent-original", RuntimeInstance: "original-daemon", RuntimeNodeId: "node-original", RuntimeEndpoint: endpoint}}, nil
			}
			runtime := &cleanupProvenanceRuntime{durableCleanupContainer: durableCleanupContainer{
				stop:   func(context.Context, string) error { stopped++; return nil },
				logs:   func(context.Context, string) (<-chan *jobsmodel.JobLog, <-chan error, error) { return nil, nil, nil },
				remove: func(context.Context, string) error { removed++; return nil },
			}}
			switch scenario {
			case "identity read before stop":
				runtime.faultAt = 1
			case "repoint before stop":
				runtime.faultAt = 1
				runtime.mismatch = true
			case "identity read after removal":
				runtime.faultAt = 2
			case "repoint after removal":
				runtime.faultAt = 2
				runtime.mismatch = true
			}
			repo := &Repository{auth: testAuth{}, svc: &Services{Jobs: jobs, CsvcForEndpoint: func(node, endpoint string) (ContainerSvc, error) {
				factories++
				if node != "node-original" || endpoint != "tcp://original:2376" {
					t.Errorf("runtime retargeted: %q/%q", node, endpoint)
				}
				return runtime, nil
			}}}
			workflow := buildContractWorkflow()
			workflow.Kind = "CONTAINER"
			err := repo.cleanupCanceledJobContainer(t.Context(), workflow, &jobspb.JobsResponse{
				Id:              "job-original",
				ContainerId:     "container-original",
				RuntimeNodeId:   "node-original",
				RuntimeEndpoint: "tcp://original:2376",
			})
			if scenario == "verified lookup" {
				if err != nil || stopped != 1 || removed != 1 || receipts != 1 {
					t.Fatalf("verified cleanup: stop=%d remove=%d receipts=%d error=%v", stopped, removed, receipts, err)
				}
				return
			}
			if status.Code(err) != codes.Unavailable || kafkapkg.ShouldCommitOnError(err) || receipts != 0 {
				t.Fatalf("unproven removal acknowledged: receipts=%d error=%v", receipts, err)
			}
			switch {
			case scenario == "lookup failure" || scenario == "routing mismatch":
				if factories != 0 {
					t.Fatal("unproven routing contacted Docker")
				}
			case runtime.faultAt == 1:
				if stopped != 0 || removed != 0 {
					t.Fatal("wrong daemon was mutated")
				}
			default:
				if stopped != 1 || removed != 1 {
					t.Fatal("post-removal proof check was bypassed")
				}
			}
		})
	}
}
