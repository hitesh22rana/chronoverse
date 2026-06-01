package executor

import (
	"context"

	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/heartbeat"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

// executeHeartbeatWorkflow executes the HEARTBEAT workflow.
func (r *Repository) executeHeartbeatWorkflow(ctx context.Context, workflow *workflowspb.GetWorkflowByIDResponse) error {
	details, err := heartbeat.ExtractAndValidateHeartbeatDetails(workflow.GetPayload())
	if err != nil {
		return err
	}

	return r.svc.Hsvc.Execute(ctx, details.TimeOut, details.Endpoint, details.ExpectedStatusCode, details.Headers)
}
