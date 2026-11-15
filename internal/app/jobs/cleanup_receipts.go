package jobs

import (
	"context"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

// GetContainerCleanupReceipt exposes internal completion evidence.
func (j *Jobs) GetContainerCleanupReceipt(ctx context.Context, req *jobspb.ContainerCleanupReceiptRequest) (*jobspb.GetContainerCleanupReceiptResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, j.cfg.Deadline)
	defer cancel()
	completed, err := j.svc.GetContainerCleanupReceipt(ctx, req)
	if err != nil {
		return nil, err
	}
	return &jobspb.GetContainerCleanupReceiptResponse{Completed: completed}, nil
}

// RecordContainerCleanupReceipt acknowledges confirmed removal, never an attempted cleanup.
func (j *Jobs) RecordContainerCleanupReceipt(ctx context.Context, req *jobspb.ContainerCleanupReceiptRequest) (*jobspb.RecordContainerCleanupReceiptResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, j.cfg.Deadline)
	defer cancel()
	if err := j.svc.RecordContainerCleanupReceipt(ctx, req); err != nil {
		return nil, err
	}
	return &jobspb.RecordContainerCleanupReceiptResponse{}, nil
}
