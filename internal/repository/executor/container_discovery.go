package executor

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

// Discovery has one reserved worker, independent of expired-lease recovery.
func (r *Repository) reconcileContainerIntents(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.RecoveryInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		r.reconcileContainerIntentBatch(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Repository) reconcileContainerIntentBatch(ctx context.Context) {
	for n := int32(0); n < min(10, max(1, r.cfg.RecoveryBatchSize)); n++ {
		if ctx.Err() != nil || !r.claimAndInspectContainerIntent(ctx) {
			return
		}
	}
}

func (r *Repository) claimAndInspectContainerIntent(ctx context.Context) bool {
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	authCtx, err := r.withAuthorization(callCtx)
	if err != nil {
		cancel()
		return false
	}
	batch, err := r.svc.Jobs.ClaimContainerIntents(authCtx, &jobspb.ContainerIntentCommand{BatchSize: 1, Token: uuid.NewString()})
	cancel()
	if err != nil {
		loggerpkg.FromContext(ctx).Warn("claim container discovery", zap.Error(err))
		return false
	}
	if len(batch.GetIntents()) == 0 {
		return false
	}
	for _, intent := range batch.GetIntents() {
		r.inspectContainerIntent(ctx, intent)
	}
	return true
}

func (r *Repository) inspectContainerIntent(ctx context.Context, intent *jobspb.ContainerIntent) {
	workCtx, cancel := context.WithTimeout(ctx, containerMutationTimeout)
	defer cancel()
	outcome := jobsmodel.ContainerInspectionAbsent
	csvc, err := r.containerSvcForEndpoint(intent.GetRuntimeNodeId(), intent.GetRuntimeEndpoint())
	var id string
	if err == nil {
		id, err = r.resolveIntentContainer(workCtx, csvc, intent)
	}
	if err == nil && id != "" {
		outcome = jobsmodel.ContainerInspectionObserved
		authCtx, authErr := r.withAuthorization(workCtx)
		if authErr != nil {
			err = authErr
		} else {
			_, err = callContainerIntent(authCtx, r.svc.Jobs.ObserveContainer, &jobspb.ContainerIntentCommand{
				IntentId:        intent.GetId(),
				JobId:           intent.GetJobId(),
				ContainerId:     id,
				RuntimeInstance: intent.GetRuntimeInstance(),
				Token:           intent.GetToken(),
				Generation:      intent.GetGeneration(),
			})
		}
	}
	reportCtx, stopReport := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stopReport()
	authCtx, authErr := r.withAuthorization(reportCtx)
	if authErr != nil {
		loggerpkg.FromContext(ctx).Warn("authorize discovery report", zap.Error(authErr))
		return
	}
	command := &jobspb.ContainerIntentCommand{IntentId: intent.GetId(), Token: intent.GetToken(), Generation: intent.GetGeneration(), Outcome: outcome}
	if err != nil {
		command.Outcome = jobsmodel.ContainerInspectionError
		command.ErrorCode = status.Code(err).String()
		if command.ErrorCode == codes.OK.String() {
			command.ErrorCode = codes.Unavailable.String()
		}
		command.ErrorMessage = containerDiagnostic(err)
	}
	if ctx.Err() != nil {
		command.Outcome = jobsmodel.ContainerInspectionInterrupted
	}
	disposition, reportErr := r.svc.Jobs.ReportContainerInspection(authCtx, command)
	if reportErr != nil {
		loggerpkg.FromContext(ctx).Warn("persist container discovery", zap.String("intent_id", intent.GetId()), zap.Error(reportErr))
	} else if disposition.GetIntent().GetQuarantined() {
		loggerpkg.FromContext(
			ctx,
		).Error("container discovery quarantined; administrative redrive required",
			zap.String("intent_id", intent.GetId()),
			zap.String("workflow_id", intent.GetWorkflowId()),
			zap.String("runtime_node_id", intent.GetRuntimeNodeId()),
			zap.String("error_code", command.GetErrorCode()),
		)
	}
}
