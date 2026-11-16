package jobs

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

func (s *Service) updateContainerCleanup(ctx context.Context, operation string, req *jobspb.ContainerCleanupAttemptRequest) (*jobspb.ContainerCleanupAttemptResponse, error) {
	if err := s.validateCleanupReceipt(&jobspb.ContainerCleanupReceiptRequest{
		JobId:           req.GetJobId(),
		ContainerId:     req.GetContainerId(),
		RuntimeInstance: req.GetRuntimeInstance(),
		IntentId:        req.GetIntentId(),
	}); err != nil {
		return nil, err
	}
	if req.GetGeneration() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "cleanup generation must be positive")
	}
	if operation == jobsmodel.CleanupOperationFail {
		if _, err := uuid.Parse(req.GetToken()); err != nil {
			return nil, status.Error(codes.InvalidArgument, "cleanup token must be a UUID")
		}
		if req.GetErrorCode() == codes.OK.String() || req.GetErrorCode() == "" || len(req.GetErrorCode()) > 32 {
			return nil, status.Error(codes.InvalidArgument, "cleanup failure code is required (maximum 32 characters)")
		}
	}
	var redrive jobsmodel.CleanupRedrive
	if operation == jobsmodel.CleanupOperationRedrive {
		if !auth.IsInternalService(ctx) {
			return nil, status.Error(codes.PermissionDenied, "administrative cleanup redrive required")
		}
		actor, actorErr := auth.ExtractSubjectFromContext(ctx)
		if actorErr != nil {
			return nil, actorErr
		}
		redrive = jobsmodel.CleanupRedrive{Actor: actor, Reason: req.GetReason()}
		if auditErr := redrive.Validate(); auditErr != nil {
			return nil, status.Error(codes.InvalidArgument, auditErr.Error())
		}
		redrive.Reason = strings.TrimSpace(redrive.Reason)
	}
	// Canonical identity must match the SQL-generated event key.
	result, err := s.repo.UpdateContainerCleanup(ctx, operation, jobsmodel.CleanupIdentity{
		JobID:           strings.ToLower(req.GetJobId()),
		ContainerID:     req.GetContainerId(),
		RuntimeInstance: req.GetRuntimeInstance(),
		IntentID:        req.GetIntentId(),
	}, req.GetGeneration(), req.GetToken(), req.GetErrorCode(), req.GetInterrupted(), redrive)
	if err != nil {
		return nil, err
	}
	return &jobspb.ContainerCleanupAttemptResponse{State: result.State, Generation: result.Generation, Attempts: result.Attempts, Token: result.Token}, nil
}

// BeginContainerCleanupAttempt grants one bounded attempt or returns durable disposition.
func (s *Service) BeginContainerCleanupAttempt(ctx context.Context, req *jobspb.ContainerCleanupAttemptRequest) (*jobspb.ContainerCleanupAttemptResponse, error) {
	return s.updateContainerCleanup(ctx, jobsmodel.CleanupOperationBegin, req)
}

// FailContainerCleanupAttempt persists failure before a worker may acknowledge quarantine.
func (s *Service) FailContainerCleanupAttempt(ctx context.Context, req *jobspb.ContainerCleanupAttemptRequest) (*jobspb.ContainerCleanupAttemptResponse, error) {
	return s.updateContainerCleanup(ctx, jobsmodel.CleanupOperationFail, req)
}

// RedriveContainerCleanup requeues the retained snapshot with a fresh execution generation.
func (s *Service) RedriveContainerCleanup(ctx context.Context, req *jobspb.ContainerCleanupAttemptRequest) (*jobspb.ContainerCleanupAttemptResponse, error) {
	return s.updateContainerCleanup(ctx, jobsmodel.CleanupOperationRedrive, req)
}
