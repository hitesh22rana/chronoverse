package jobs

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

func (s *Service) validateCleanupReceipt(req *jobspb.ContainerCleanupReceiptRequest) error {
	request := struct {
		JobID           string `validate:"required,uuid"`
		ContainerID     string `validate:"required,max=256"`
		RuntimeInstance string `validate:"required,max=256"`
		IntentID        string `validate:"required,uuid"`
	}{req.GetJobId(), req.GetContainerId(), req.GetRuntimeInstance(), req.GetIntentId()}
	if err := s.validator.Struct(&request); err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid cleanup receipt: %v", err)
	}
	return nil
}

// GetContainerCleanupReceipt checks durable removal evidence.
func (s *Service) GetContainerCleanupReceipt(ctx context.Context, req *jobspb.ContainerCleanupReceiptRequest) (bool, error) {
	if err := s.validateCleanupReceipt(req); err != nil {
		return false, err
	}
	return s.repo.GetContainerCleanupReceipt(ctx, jobsmodel.CleanupIdentity{
		JobID:           req.GetJobId(),
		ContainerID:     req.GetContainerId(),
		RuntimeInstance: req.GetRuntimeInstance(),
		IntentID:        req.GetIntentId(),
	})
}

// RecordContainerCleanupReceipt records confirmed removal for internal workers.
func (s *Service) RecordContainerCleanupReceipt(ctx context.Context, req *jobspb.ContainerCleanupReceiptRequest) error {
	if err := s.validateCleanupReceipt(req); err != nil {
		return err
	}
	return s.repo.RecordContainerCleanupReceipt(ctx, jobsmodel.CleanupIdentity{
		JobID:           req.GetJobId(),
		ContainerID:     req.GetContainerId(),
		RuntimeInstance: req.GetRuntimeInstance(),
		IntentID:        req.GetIntentId(),
	})
}
