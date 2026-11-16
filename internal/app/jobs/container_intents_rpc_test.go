package jobs_test

import (
	"context"

	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"

	jobsmock "github.com/hitesh22rana/chronoverse/internal/app/jobs/mock"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

// These RPCs carry authority and audit metadata through the authenticated server;
// the shared transport contract checks rejection, service errors and deadline cleanup.
func containerIntentRPCContracts() []leaseRPCContract {
	request := &jobspb.ContainerIntentCommand{
		JobId: leaseContractJobID, IntentId: leaseContractWorkflowID, LeaseToken: "owned-lease",
		RuntimeInstance: "original-daemon", RequestHash: "frozen", ContainerId: "observed-id",
		Outcome: "ERROR", ErrorCode: "Unavailable", ErrorMessage: "original diagnostic", Token: leaseContractSecondJobID,
		Reason: "daemon restored", Attempt: 2, BatchSize: 4, Generation: 3, Stage: "CREATE", WorkflowGeneration: 5,
	}
	response := &jobspb.ContainerIntentResponse{Granted: true, Intent: &jobspb.ContainerIntent{
		Id: leaseContractWorkflowID, JobId: leaseContractJobID, RuntimeInstance: "original-daemon", Closed: true, Generation: 3, HeldCapacity: true,
	}}
	var contracts []leaseRPCContract
	for _, tc := range []struct {
		method string
		expect func(*jobsmock.MockService) *gomock.Call
	}{
		{"EnsureContainerIntent", func(s *jobsmock.MockService) *gomock.Call {
			return s.EXPECT().EnsureContainerIntent(gomock.Any(), gomock.Any())
		}},
		{"GetContainerIntent", func(s *jobsmock.MockService) *gomock.Call {
			return s.EXPECT().GetContainerIntent(gomock.Any(), gomock.Any())
		}},
		{"ReserveContainerCreate", func(s *jobsmock.MockService) *gomock.Call {
			return s.EXPECT().ReserveContainerCreate(gomock.Any(), gomock.Any())
		}},
		{"ObserveContainer", func(s *jobsmock.MockService) *gomock.Call {
			return s.EXPECT().ObserveContainer(gomock.Any(), gomock.Any())
		}},
		{"ReserveContainerStart", func(s *jobsmock.MockService) *gomock.Call {
			return s.EXPECT().ReserveContainerStart(gomock.Any(), gomock.Any())
		}},
		{"CloseContainerIntent", func(s *jobsmock.MockService) *gomock.Call {
			return s.EXPECT().CloseContainerIntent(gomock.Any(), gomock.Any())
		}},
		{"ReportContainerInspection", func(s *jobsmock.MockService) *gomock.Call {
			return s.EXPECT().ReportContainerInspection(gomock.Any(), gomock.Any())
		}},
		{"RedriveContainerIntent", func(s *jobsmock.MockService) *gomock.Call {
			return s.EXPECT().RedriveContainerIntent(gomock.Any(), gomock.Any())
		}},
	} {
		contracts = append(contracts, leaseRPCContract{
			method: tc.method, request: request, response: response,
			expect: func(svc *jobsmock.MockService, check func(context.Context, proto.Message), failure error) {
				tc.expect(svc).DoAndReturn(func(ctx context.Context, req *jobspb.ContainerIntentCommand) (*jobspb.ContainerIntentResponse, error) {
					check(ctx, req)
					return response, failure
				})
			},
		})
	}
	listResponse := &jobspb.ListContainerIntentsResponse{Intents: []*jobspb.ContainerIntent{response.Intent}}
	for _, tc := range []struct {
		method string
		expect func(*jobsmock.MockService) *gomock.Call
	}{
		{"ClaimContainerIntents", func(s *jobsmock.MockService) *gomock.Call {
			return s.EXPECT().ClaimContainerIntents(gomock.Any(), gomock.Any())
		}},
		{"ListContainerIntents", func(s *jobsmock.MockService) *gomock.Call {
			return s.EXPECT().ListContainerIntents(gomock.Any(), gomock.Any())
		}},
	} {
		contracts = append(contracts, leaseRPCContract{
			method: tc.method, request: request, response: listResponse,
			expect: func(svc *jobsmock.MockService, check func(context.Context, proto.Message), failure error) {
				tc.expect(svc).DoAndReturn(func(ctx context.Context, req *jobspb.ContainerIntentCommand) (*jobspb.ListContainerIntentsResponse, error) {
					check(ctx, req)
					return listResponse, failure
				})
			},
		})
	}
	return contracts
}
