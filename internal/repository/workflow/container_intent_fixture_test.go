//nolint:testpackage // Fixtures for immutable cleanup proof lookup.
package workflow

import (
	"context"

	"google.golang.org/grpc"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

func (c *testJobsClient) GetContainerIntent(_ context.Context, req *jobspb.ContainerIntentCommand, _ ...grpc.CallOption) (*jobspb.ContainerIntentResponse, error) {
	return &jobspb.ContainerIntentResponse{
		Intent: &jobspb.ContainerIntent{
			Id: "00000000-0000-4000-8000-000000000015", JobId: req.GetJobId(),
			ContainerId: req.GetContainerId(), RuntimeInstance: "test-daemon",
			RuntimeEndpoint: "tcp://docker-proxy:2375",
		},
	}, nil
}
