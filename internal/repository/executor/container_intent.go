package executor

import (
	"context"
	"errors"
	"strconv"
	"time"
	"unicode/utf8"

	dockercontainer "github.com/moby/moby/api/types/container"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/terminalreason"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

const (
	containerMutationTimeout = 30 * time.Second
	containerControlTimeout  = 5 * time.Second
)

//nolint:gocritic // Copy the immutable creation snapshot before granting mutation authority.
func (r *Repository) executeContainerIntent(
	ctx context.Context,
	csvc ContainerSvc,
	jobID,
	leaseToken,
	runtimeNodeID string,
	attempt int32,
	workflow *workflowspb.GetWorkflowByIDResponse,
	request container.CreateRequest,
) (containerID string, resultErr error) {
	authCtx, err := r.withAuthorization(ctx)
	if err != nil {
		return "", terminalreason.Wrap(terminalreason.SystemError, err)
	}
	command, intent, err := r.prepareContainerIntent(authCtx, csvc, jobID, leaseToken, runtimeNodeID, attempt, workflow, request)
	if err != nil {
		return "", err
	}
	stage := "RESERVE_CREATE"
	defer func() {
		if resultErr != nil {
			resultErr = r.reportContainerIntentFailure(ctx, command, stage, resultErr)
		}
	}()
	grant, err := callContainerIntent(authCtx, r.svc.Jobs.ReserveContainerCreate, command)
	if err != nil {
		return "", terminalreason.Wrap(terminalreason.SystemError, err)
	}
	if !grant.GetGranted() {
		return "", terminalreason.Wrap(terminalreason.SystemError, status.Error(codes.Aborted, "container create permission was already consumed"))
	}
	stage = "CREATE"
	request.Name, request.IntentID, request.JobID, request.Attempt = intent.GetName(), intent.GetId(), jobID, attempt
	containerID, err = r.createIntentContainer(ctx, csvc, intent, request)
	if err != nil {
		return "", err
	}
	stage = "OBSERVE"
	command.ContainerId = containerID
	if _, err = callContainerIntent(authCtx, r.svc.Jobs.ObserveContainer, command); err != nil {
		return "", terminalreason.Wrap(terminalreason.SystemError, err)
	}
	stage = "ATTACH"
	if err = r.attachJobContainer(ctx, jobID, leaseToken, containerID, runtimeNodeID); err != nil {
		return containerID, terminalreason.Wrap(terminalreason.SystemError, err)
	}
	if inspectErr := verifyUnstartedContainer(ctx, csvc, containerID); inspectErr != nil {
		return containerID, terminalreason.Wrap(terminalreason.SystemError, inspectErr)
	}
	stage = "RESERVE_START"
	grant, err = callContainerIntent(authCtx, r.svc.Jobs.ReserveContainerStart, command)
	if err != nil {
		return containerID, terminalreason.Wrap(terminalreason.SystemError, err)
	}
	if !grant.GetGranted() {
		return containerID, terminalreason.Wrap(terminalreason.SystemError, status.Error(codes.Aborted, "container start permission was already consumed"))
	}
	stage = "START"
	logs, errs, err := csvc.Start(ctx, request.Timeout, containerID)
	if err != nil {
		return containerID, err
	}
	stage = "EXECUTION"
	return containerID, r.processContainerExecution(ctx, jobID, attempt, workflow, logs, errs)
}

func containerInstance(ctx context.Context, csvc ContainerSvc) (string, error) {
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	instance, err := csvc.RuntimeInstance(readCtx)
	if err != nil {
		return "", terminalreason.Wrap(terminalreason.SystemError, err)
	}
	if instance == "" {
		return "", status.Error(codes.Unavailable, "runtime instance identity is missing")
	}
	return instance, nil
}

func (r *Repository) resolveIntentContainer(ctx context.Context, csvc ContainerSvc, intent *jobspb.ContainerIntent) (string, error) {
	return r.verifyIntentContainer(ctx, csvc, intent, intent.GetName())
}

func (r *Repository) verifyIntentContainer(
	ctx context.Context,
	csvc ContainerSvc,
	intent *jobspb.ContainerIntent,
	id string,
) (
	string,
	error,
) {
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	instance, err := containerInstance(readCtx, csvc)
	if err != nil {
		return "", err
	}
	if instance != intent.GetRuntimeInstance() {
		return "", status.Error(codes.Unavailable, "runtime instance changed during container observation")
	}
	state, err := csvc.Inspect(readCtx, id)
	confirmed, identityErr := containerInstance(readCtx, csvc)
	if identityErr != nil {
		return "", identityErr
	}
	if confirmed != intent.GetRuntimeInstance() {
		return "", status.Error(codes.Unavailable, "runtime instance changed during container observation")
	}
	if status.Code(err) == codes.NotFound {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if state == nil || state.ID == "" ||
		state.Labels[container.IntentIDLabel] != intent.GetId() ||
		state.Labels[container.JobIDLabel] != intent.GetJobId() ||
		state.Labels[container.AttemptLabel] != strconv.Itoa(int(intent.GetAttempt())) {
		return "", status.Error(codes.Unavailable, "container ownership does not match intent; administrative repair required")
	}
	return state.ID, nil
}

func containerDiagnostic(err error) string {
	runes := []rune(renderExecutionDiagnostic(status.Convert(err).Message(), 2048*utf8.UTFMax))
	if len(runes) > 2048 {
		runes = runes[:2048]
	}
	return string(runes)
}

//nolint:gocritic // The immutable request snapshot is intentionally copied across the authority boundary.
func (r *Repository) prepareContainerIntent(
	ctx context.Context,
	csvc ContainerSvc,
	jobID, leaseToken, runtimeNodeID string,
	attempt int32,
	workflow *workflowspb.GetWorkflowByIDResponse,
	request container.CreateRequest,
) (*jobspb.ContainerIntentCommand, *jobspb.ContainerIntent, error) {
	instance, err := containerInstance(ctx, csvc)
	if err != nil {
		return nil, nil, terminalreason.Wrap(terminalreason.SystemError, err)
	}
	// The fingerprint contains only strings, string slices and integers.
	//nolint:errcheck // These concrete values cannot fail JSON encoding.
	hash, _ := idempotency.HashCanonical(map[string]any{"image": request.Image, "cmd": request.Cmd, "env": request.Env, "timeout": int64(request.Timeout), "generation": workflow.GetGeneration()})
	command := &jobspb.ContainerIntentCommand{JobId: jobID, LeaseToken: leaseToken, Attempt: attempt, WorkflowGeneration: workflow.GetGeneration(), RuntimeInstance: instance, RequestHash: hash}
	ensured, err := callContainerIntent(ctx, r.svc.Jobs.EnsureContainerIntent, command)
	if err != nil {
		return nil, nil, terminalreason.Wrap(terminalreason.SystemError, err)
	}
	intent := ensured.GetIntent()
	if intent.GetId() == "" || intent.GetClosed() || intent.GetRuntimeInstance() != instance || intent.GetRuntimeNodeId() != runtimeNodeID {
		return nil, nil, status.Error(codes.Aborted, "container intent is not executable on this runtime")
	}
	command.IntentId = intent.GetId()

	return command, intent, nil
}

//nolint:gocritic // The adapter receives its own immutable request snapshot.
func (r *Repository) createIntentContainer(
	ctx context.Context,
	csvc ContainerSvc,
	intent *jobspb.ContainerIntent,
	request container.CreateRequest,
) (string, error) {
	mutationCtx, cancel := context.WithTimeout(ctx, containerMutationTimeout)
	containerID, err := csvc.Create(mutationCtx, request)
	cancel()
	if err != nil {
		if container.CreationOutcome(err) == container.CreationOutcomeRejected {
			return "", err
		}
		// Resolve a lost reply by identity; absence never proves rejection.
		observed, resolveErr := r.resolveIntentContainer(ctx, csvc, intent)
		if resolveErr != nil {
			return "", terminalreason.Wrap(terminalreason.SystemError, resolveErr)
		}
		if observed == "" {
			return "", err
		}
		containerID = observed
	}
	verified, err := r.verifyIntentContainer(ctx, csvc, intent, containerID)
	if err != nil {
		return "", terminalreason.Wrap(terminalreason.SystemError, err)
	}
	if verified == "" {
		return "", terminalreason.Wrap(terminalreason.SystemError, status.Error(codes.Unavailable, "created container is not observable"))
	}
	containerID = verified

	return containerID, nil
}

func (r *Repository) reportContainerIntentFailure(
	ctx context.Context,
	command *jobspb.ContainerIntentCommand,
	stage string,
	workErr error,
) error {
	reportCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	reportAuth, reportErr := r.withAuthorization(reportCtx)
	if reportErr == nil {
		_, reportErr = r.svc.Jobs.CloseContainerIntent(reportAuth, &jobspb.ContainerIntentCommand{
			JobId: command.GetJobId(), LeaseToken: command.GetLeaseToken(), IntentId: command.GetIntentId(),
			Attempt: command.GetAttempt(), Outcome: container.CreationOutcome(workErr),
			ErrorCode: status.Code(workErr).String(), ErrorMessage: containerDiagnostic(workErr), Stage: stage,
		})
	}
	if reportErr == nil {
		return workErr
	}
	loggerpkg.FromContext(ctx).Warn("persist container intent failure",
		zap.String("intent_id", command.GetIntentId()),
		zap.Error(reportErr),
		zap.NamedError("operation_error", workErr),
	)
	// Settlement also closes the intent; keep the original outcome and diagnostic.
	return workErr
}

func verifyUnstartedContainer(ctx context.Context, csvc ContainerSvc, containerID string) error {
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state, err := csvc.Inspect(readCtx, containerID)
	if err != nil {
		return err
	}
	if state == nil || state.Running || state.Status != string(dockercontainer.StateCreated) {
		return status.Error(codes.Unavailable, "container ran before its start reservation")
	}
	return nil
}

// The grant and observation RPCs need their own deadline even while a live lease
// keeps renewing: database lock contention must not suspend the protocol forever.
func callContainerIntent(
	ctx context.Context,
	call func(context.Context, *jobspb.ContainerIntentCommand, ...grpc.CallOption) (*jobspb.ContainerIntentResponse, error),
	command *jobspb.ContainerIntentCommand,
) (*jobspb.ContainerIntentResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, containerControlTimeout)
	defer cancel()
	result, err := call(ctx, command)
	if ctx.Err() != nil {
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		err = status.FromContextError(err).Err()
	}
	return result, err
}
