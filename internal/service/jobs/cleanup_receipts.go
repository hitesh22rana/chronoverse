package jobs

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

func (s *Service) validateCleanupReceipt(req *jobspb.ContainerCleanupReceiptRequest) error {
	request := struct {
		JobID       string `validate:"required,uuid"`
		ContainerID string `validate:"required,max=256"`
	}{req.GetJobId(), req.GetContainerId()}
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
	return s.repo.GetContainerCleanupReceipt(ctx, req.GetJobId(), req.GetContainerId())
}

// RecordContainerCleanupReceipt records confirmed removal for internal workers.
func (s *Service) RecordContainerCleanupReceipt(ctx context.Context, req *jobspb.ContainerCleanupReceiptRequest) error {
	if err := s.validateCleanupReceipt(req); err != nil {
		return err
	}
	return s.repo.RecordContainerCleanupReceipt(ctx, req.GetJobId(), req.GetContainerId())
}
