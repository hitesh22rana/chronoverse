package executor

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

func (r *Repository) executeHeartbeatWorkflow(ctx context.Context, jobID string, workflow *workflowspb.GetWorkflowByIDResponse) error {
	//nolint:errcheck // Ignore the error as we don't want to block the job execution
	withRetry(func() error {
		// Issue necessary headers and tokens for authorization
		// This context uses the parent context
		//nolint:errcheck // Ignore the error as we don't want to block the job execution
		jobCtx, _ := r.withAuthorization(ctx)
		// Update the job status with the container ID
		if _, err := r.svc.Jobs.UpdateJobStatus(jobCtx, &jobspb.UpdateJobStatusRequest{
			Id:     jobID,
			Status: jobsmodel.JobStatusRunning.ToString(),
		}); err != nil {
			return status.Errorf(codes.Internal, "failed to update job status: %v", err)
		}
		return nil
	})

	return r.svc.Hsvc.Execute(ctx, workflow.GetPayload())
}
