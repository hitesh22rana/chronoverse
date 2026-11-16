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

const intentExecutionSubject = "internal/executor"

func intentProto(i *jobsmodel.ContainerIntent) *jobspb.ContainerIntent {
	if i == nil {
		return nil
	}
	return &jobspb.ContainerIntent{
		Id:                     i.ID,
		ImageReference:         i.ImageReference,
		WorkflowGeneration:     i.WorkflowGeneration,
		Stage:                  i.Stage,
		InspectionErrorCode:    i.InspectionErrorCode,
		InspectionErrorMessage: i.InspectionErrorMessage,
		Quarantined:            i.Quarantined,
		InspectionErrors:       i.InspectionErrors,
		HeldCapacity:           i.HeldCapacity,
		JobId:                  i.JobID,
		WorkflowId:             i.WorkflowID,
		UserId:                 i.UserID,
		Name:                   i.Name,
		RuntimeNodeId:          i.RuntimeNodeID,
		RuntimeEndpoint:        i.RuntimeEndpoint,
		RuntimeInstance:        i.RuntimeInstance,
		RequestHash:            i.RequestHash,
		ContainerId:            i.ContainerID,
		Outcome:                i.Outcome,
		ErrorCode:              i.ErrorCode,
		ErrorMessage:           i.ErrorMessage,
		Token:                  i.Token,
		NextInspectionAt:       i.NextInspectionAt,
		LastInspectionAt:       i.LastInspectionAt,
		Attempt:                i.Attempt,
		Generation:             i.Generation,
		CreateReserved:         i.CreateReserved,
		StartReserved:          i.StartReserved,
		Closed:                 i.Closed,
		LogRetention:           i.LogRetention,
	}
}

func intentCommand(req *jobspb.ContainerIntentCommand) *jobsmodel.ContainerIntentCommand {
	return &jobsmodel.ContainerIntentCommand{
		WorkflowGeneration: req.GetWorkflowGeneration(),
		Stage:              req.GetStage(),
		JobID:              req.GetJobId(),
		LeaseToken:         req.GetLeaseToken(),
		IntentID:           req.GetIntentId(),
		RuntimeInstance:    req.GetRuntimeInstance(),
		RequestHash:        req.GetRequestHash(),
		ContainerID:        req.GetContainerId(),
		Outcome:            req.GetOutcome(),
		ErrorCode:          req.GetErrorCode(),
		ErrorMessage:       req.GetErrorMessage(),
		Token:              req.GetToken(),
		Reason:             req.GetReason(),
		Attempt:            req.GetAttempt(),
		BatchSize:          req.GetBatchSize(),
		Generation:         req.GetGeneration(),
	}
}

func validateIntentCommand(ctx context.Context, op string, req *jobspb.ContainerIntentCommand) error {
	if !auth.IsInternalService(ctx) {
		return status.Error(codes.PermissionDenied, "internal container lifecycle operation")
	}
	if req == nil {
		return status.Error(codes.InvalidArgument, "container intent request required")
	}
	if err := validateIntentBounds(req); err != nil {
		return err
	}
	switch op {
	case jobsmodel.ContainerIntentOperationEnsure:
		return validateEnsureCommand(req)
	case jobsmodel.ContainerIntentOperationReserveCreate, jobsmodel.ContainerIntentOperationReserveStart, jobsmodel.ContainerIntentOperationClose:
		return validateLiveAuthorityCommand(req)
	case jobsmodel.ContainerIntentOperationGet:
		return validateGetCommand(req)
	case jobsmodel.ContainerIntentOperationObserve:
		return validateObserveCommand(req)
	case jobsmodel.ContainerIntentOperationReport:
		return validateReportCommand(req)
	case jobsmodel.ContainerIntentOperationRedrive:
		return validateRedriveCommand(req)
	case jobsmodel.ContainerIntentOperationList:
		return nil
	default:
		return status.Error(codes.InvalidArgument, "unknown container intent operation")
	}
}

func validateIntentBounds(req *jobspb.ContainerIntentCommand) error {
	if req.GetBatchSize() < 0 || req.GetBatchSize() > 500 {
		return status.Error(codes.InvalidArgument, "invalid container intent batch size")
	}
	if err := validateOptionalUUID(req.GetToken(), "invalid discovery token"); err != nil {
		return err
	}
	if len(req.GetStage()) > 32 || len(req.GetOutcome()) > 32 {
		return status.Error(codes.InvalidArgument, "invalid container outcome metadata")
	}
	if err := validateOptionalUUID(req.GetJobId(), "invalid job id"); err != nil {
		return err
	}
	if err := validateOptionalUUID(req.GetIntentId(), "invalid intent id"); err != nil {
		return err
	}
	if len(req.GetErrorMessage()) > 8192 || len(req.GetReason()) > 2048 ||
		len(req.GetRuntimeInstance()) > 256 || len(req.GetRequestHash()) > 256 {
		return status.Error(codes.InvalidArgument, "container intent fields exceed limits")
	}
	return nil
}

func validateOptionalUUID(value, message string) error {
	if value == "" {
		return nil
	}
	if _, err := uuid.Parse(value); err != nil {
		return status.Error(codes.InvalidArgument, message)
	}
	return nil
}

func validateEnsureCommand(req *jobspb.ContainerIntentCommand) error {
	if req.GetJobId() == "" || req.GetLeaseToken() == "" || req.GetAttempt() <= 0 ||
		req.GetWorkflowGeneration() <= 0 || req.GetRuntimeInstance() == "" || req.GetRequestHash() == "" {
		return status.Error(codes.InvalidArgument, "intent execution identity required")
	}
	return nil
}

func validateLiveAuthorityCommand(req *jobspb.ContainerIntentCommand) error {
	if req.GetIntentId() == "" || req.GetJobId() == "" || req.GetLeaseToken() == "" || req.GetAttempt() <= 0 {
		return status.Error(codes.InvalidArgument, "live container authority required")
	}
	return nil
}

func validateGetCommand(req *jobspb.ContainerIntentCommand) error {
	if req.GetIntentId() == "" && (req.GetJobId() == "" || (req.GetAttempt() <= 0 && req.GetContainerId() == "")) {
		return status.Error(codes.InvalidArgument, "container lookup identity required")
	}
	return nil
}

func validateObserveCommand(req *jobspb.ContainerIntentCommand) error {
	if req.GetIntentId() == "" || req.GetRuntimeInstance() == "" || len(req.GetContainerId()) != 64 {
		return status.Error(codes.InvalidArgument, "verified materialization identity required")
	}
	return nil
}

func validateReportCommand(req *jobspb.ContainerIntentCommand) error {
	if req.GetIntentId() == "" || req.GetToken() == "" || req.GetGeneration() <= 0 {
		return status.Error(codes.InvalidArgument, "discovery ownership required")
	}
	switch req.GetOutcome() {
	case jobsmodel.ContainerInspectionAbsent, jobsmodel.ContainerInspectionObserved, jobsmodel.ContainerInspectionError, jobsmodel.ContainerInspectionInterrupted:
		return nil
	default:
		return status.Error(codes.InvalidArgument, "invalid discovery outcome")
	}
}

func validateRedriveCommand(req *jobspb.ContainerIntentCommand) error {
	if req.GetIntentId() == "" || strings.TrimSpace(req.GetReason()) == "" || req.GetGeneration() <= 0 {
		return status.Error(codes.InvalidArgument, "redrive generation and reason required")
	}
	return nil
}

func requireExecutionPrincipal(ctx context.Context) error {
	subject, err := auth.ExtractSubjectFromContext(ctx)
	if err != nil {
		return err
	}
	if subject != intentExecutionSubject {
		return status.Error(codes.PermissionDenied, "execution principal required")
	}
	return nil
}

func (s *Service) updateContainerIntent(ctx context.Context, op string, command *jobsmodel.ContainerIntentCommand) (*jobspb.ContainerIntentResponse, error) {
	result, err := s.repo.UpdateContainerIntent(ctx, op, command)
	if err != nil {
		return nil, err
	}
	return &jobspb.ContainerIntentResponse{Intent: intentProto(result.Intent), Granted: result.Granted}, nil
}

func (s *Service) executeContainerIntent(ctx context.Context, op string, req *jobspb.ContainerIntentCommand) (*jobspb.ContainerIntentResponse, error) {
	if err := validateIntentCommand(ctx, op, req); err != nil {
		return nil, err
	}
	if err := requireExecutionPrincipal(ctx); err != nil {
		return nil, err
	}
	return s.updateContainerIntent(ctx, op, intentCommand(req))
}

// EnsureContainerIntent creates or returns the lease-fenced intent for one execution attempt.
func (s *Service) EnsureContainerIntent(ctx context.Context, req *jobspb.ContainerIntentCommand) (*jobspb.ContainerIntentResponse, error) {
	return s.executeContainerIntent(ctx, jobsmodel.ContainerIntentOperationEnsure, req)
}

// GetContainerIntent returns a container intent to internal administration.
func (s *Service) GetContainerIntent(ctx context.Context, req *jobspb.ContainerIntentCommand) (*jobspb.ContainerIntentResponse, error) {
	if err := validateIntentCommand(ctx, jobsmodel.ContainerIntentOperationGet, req); err != nil {
		return nil, err
	}
	return s.updateContainerIntent(ctx, jobsmodel.ContainerIntentOperationGet, intentCommand(req))
}

// ReserveContainerCreate grants at most one container creation for the held execution lease.
func (s *Service) ReserveContainerCreate(ctx context.Context, req *jobspb.ContainerIntentCommand) (*jobspb.ContainerIntentResponse, error) {
	return s.executeContainerIntent(ctx, jobsmodel.ContainerIntentOperationReserveCreate, req)
}

// ObserveContainer records a verified materialization for the held execution lease.
func (s *Service) ObserveContainer(ctx context.Context, req *jobspb.ContainerIntentCommand) (*jobspb.ContainerIntentResponse, error) {
	return s.executeContainerIntent(ctx, jobsmodel.ContainerIntentOperationObserve, req)
}

// ReserveContainerStart grants at most one start after the immutable container is attached.
func (s *Service) ReserveContainerStart(ctx context.Context, req *jobspb.ContainerIntentCommand) (*jobspb.ContainerIntentResponse, error) {
	return s.executeContainerIntent(ctx, jobsmodel.ContainerIntentOperationReserveStart, req)
}

// CloseContainerIntent closes the intent with its outcome, diagnostics, and stage.
func (s *Service) CloseContainerIntent(ctx context.Context, req *jobspb.ContainerIntentCommand) (*jobspb.ContainerIntentResponse, error) {
	return s.executeContainerIntent(ctx, jobsmodel.ContainerIntentOperationClose, req)
}

// ReportContainerInspection settles a fenced discovery inspection and its diagnostics.
func (s *Service) ReportContainerInspection(ctx context.Context, req *jobspb.ContainerIntentCommand) (*jobspb.ContainerIntentResponse, error) {
	return s.executeContainerIntent(ctx, jobsmodel.ContainerIntentOperationReport, req)
}

// RedriveContainerIntent clears quarantine and audits the validated administrative principal.
func (s *Service) RedriveContainerIntent(ctx context.Context, req *jobspb.ContainerIntentCommand) (*jobspb.ContainerIntentResponse, error) {
	if err := validateIntentCommand(ctx, jobsmodel.ContainerIntentOperationRedrive, req); err != nil {
		return nil, err
	}
	actor, err := auth.ExtractSubjectFromContext(ctx)
	if err != nil {
		return nil, err
	}
	command := intentCommand(req)
	command.Actor = actor
	return s.updateContainerIntent(ctx, jobsmodel.ContainerIntentOperationRedrive, command)
}

func (s *Service) listContainerIntents(ctx context.Context, req *jobspb.ContainerIntentCommand, claim bool) (*jobspb.ListContainerIntentsResponse, error) {
	intents, err := s.repo.ListContainerIntents(ctx, intentCommand(req), claim)
	if err != nil {
		return nil, err
	}
	result := &jobspb.ListContainerIntentsResponse{}
	for _, i := range intents {
		result.Intents = append(result.Intents, intentProto(i))
	}
	return result, nil
}

// ClaimContainerIntents leases due intents for the discovery execution principal.
func (s *Service) ClaimContainerIntents(ctx context.Context, req *jobspb.ContainerIntentCommand) (*jobspb.ListContainerIntentsResponse, error) {
	if err := validateIntentCommand(ctx, jobsmodel.ContainerIntentOperationList, req); err != nil {
		return nil, err
	}
	if err := requireExecutionPrincipal(ctx); err != nil {
		return nil, err
	}
	return s.listContainerIntents(ctx, req, true)
}

// ListContainerIntents returns intents to internal administration without leasing them.
func (s *Service) ListContainerIntents(ctx context.Context, req *jobspb.ContainerIntentCommand) (*jobspb.ListContainerIntentsResponse, error) {
	if err := validateIntentCommand(ctx, jobsmodel.ContainerIntentOperationList, req); err != nil {
		return nil, err
	}
	return s.listContainerIntents(ctx, req, false)
}
