package executor

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	delimiter   = '$'
	authSubject = "internal/executor"

	// Statuses for the jobs.
	statusRunning   = "RUNNING"
	statusCompleted = "COMPLETED"
	statusFailed    = "FAILED"
	statusCanceled  = "CANCELED"

	// Workflow names.
	workflowHeartBeat = "HEARTBEAT"
	workflowContainer = "CONTAINER"

	containerWorkflowExecutionTimeout = 10 * time.Second
)

// Workflow represents a workflow that can be executed.
type Workflow interface {
	Execute(ctx context.Context) error
}

// ContainerSvc represents the container service.
type ContainerSvc interface {
	Execute(ctx context.Context, timeout time.Duration, image string, cmd []string) (<-chan string, <-chan error, error)
}

// HeartBeatSvc represents the heartbeat service.
type HeartBeatSvc interface {
	Execute(ctx context.Context, payload string) error
}

// Services represents the services used by the executor.
type Services struct {
	Workflows workflowspb.WorkflowsServiceClient
	Jobs      jobspb.JobsServiceClient
	Csvc      ContainerSvc
	Hsvc      HeartBeatSvc
}

// Config represents the repository constants configuration.
type Config struct {
	ParallelismLimit int
}

// Repository provides executor repository.
type Repository struct {
	tp   trace.Tracer
	cfg  *Config
	kfk  *kgo.Client
	auth auth.IAuth
	svc  *Services
}

// New creates a new executor repository.
func New(cfg *Config, auth auth.IAuth, svc *Services, kfk *kgo.Client) *Repository {
	return &Repository{
		tp:   otel.Tracer(svcpkg.Info().GetName()),
		cfg:  cfg,
		auth: auth,
		svc:  svc,
		kfk:  kfk,
	}
}

// Run starts the executor.
func (r *Repository) Run(ctx context.Context) error {
	logger := loggerpkg.FromContext(ctx)

	for {
		// Check context cancellation before processing
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Continue processing
		}

		fetches := r.kfk.PollFetches(ctx)
		if fetches.IsClientClosed() {
			return status.Error(codes.Canceled, "client closed")
		}

		if fetches.Empty() {
			continue
		}

		iter := fetches.RecordIter()
		for _, fetchErr := range fetches.Errors() {
			logger.Error("error while fetching records",
				zap.String("topic", fetchErr.Topic),
				zap.Int32("partition", fetchErr.Partition),
				zap.Error(fetchErr.Err),
			)
			continue
		}

		// Error group for running multiple goroutines
		eg, groupCtx := errgroup.WithContext(ctx)
		eg.SetLimit(r.cfg.ParallelismLimit)

		for !iter.Done() {
			// Process the record in a separate goroutine
			eg.Go(func(record *kgo.Record) func() error {
				return func() error {
					ctxWithTrace, span := r.tp.Start(groupCtx, "executor.Run")
					defer span.End()

					// Execute the run workflow
					if err := r.runWorkflow(ctxWithTrace, string(record.Value)); err != nil {
						logger.Error(
							"run workflow execution failed",
							zap.Any("ctx", ctxWithTrace),
							zap.Error(err),
						)
					}

					// Commit the record even if the workflow workflow fails to avoid reprocessing
					if err := r.kfk.CommitRecords(ctxWithTrace, record); err != nil {
						logger.Error(
							"failed to commit record",
							zap.Any("ctx", ctxWithTrace),
							zap.String("topic", record.Topic),
							zap.Int64("offset", record.Offset),
							zap.Int32("partition", record.Partition),
							zap.String("message", string(record.Value)),
							zap.Error(err),
						)
					} else {
						logger.Info("record processed and committed successfully",
							zap.Any("ctx", ctxWithTrace),
							zap.String("topic", record.Topic),
							zap.Int64("offset", record.Offset),
							zap.Int32("partition", record.Partition),
							zap.String("message", string(record.Value)),
						)
					}

					return nil
				}
			}(iter.Next()))
		}

		// Wait for all the goroutines to finish
		if err := eg.Wait(); err != nil {
			logger.Error("error while running goroutines", zap.Error(err))
		}
	}
}

// runWorkflow runs the executor workflow.
//
//nolint:gocyclo // This function is complex and has multiple responsibilities.
func (r *Repository) runWorkflow(ctx context.Context, recordValue string) error {
	// Extract the data from the record value
	jobID, workflowID, lastScheduledAt, err := extractDataFromRecordValue(recordValue)
	if err != nil {
		return err
	}

	// Issue necessary headers and tokens for authorization
	ctx, err = r.withAuthorization(ctx)
	if err != nil {
		return err
	}

	// Get the workflow details
	workflow, err := r.svc.Workflows.GetWorkflowByID(ctx, &workflowspb.GetWorkflowByIDRequest{
		Id: workflowID,
	})
	if err != nil {
		return err
	}

	// Early return idempotency checks
	// Ensure the workflow build status is COMPLETED
	if workflow.GetBuildStatus() != statusCompleted {
		return status.Error(codes.FailedPrecondition, "workflow build status is not COMPLETED")
	}

	// Ensure the workflow is not already terminated
	terminatedAt := workflow.GetTerminatedAt()
	if terminatedAt != "" {
		// If the workflow is already terminated, do not execute the workflow and update the job status to CANCELED
		if _, _err := r.svc.Jobs.UpdateJobStatus(ctx, &jobspb.UpdateJobStatusRequest{
			Id:     jobID,
			Status: statusCanceled,
		}); _err != nil {
			return _err
		}

		return status.Error(codes.FailedPrecondition, "workflow is already terminated")
	}

	// Schedule a new job based on the last scheduledAt time and interval accordingly
	if _, _err := r.svc.Jobs.ScheduleJob(ctx, &jobspb.ScheduleJobRequest{
		WorkflowId:  workflowID,
		UserId:      workflow.GetUserId(),
		ScheduledAt: lastScheduledAt.Add(time.Minute * time.Duration(workflow.GetInterval())).Format(time.RFC3339Nano),
	}); _err != nil {
		return _err
	}

	// Update the jov status from QUEUED to RUNNING
	if _, _err := r.svc.Jobs.UpdateJobStatus(ctx, &jobspb.UpdateJobStatusRequest{
		Id:     jobID,
		Status: statusRunning,
	}); _err != nil {
		return _err
	}

	//nolint:nestif // This nested if statement is necessary for the workflow execution
	if err := r.executeWorkflow(ctx, jobID, workflow); err != nil {
		// Update the job status from RUNNING to FAILED
		if _, _err := r.svc.Jobs.UpdateJobStatus(ctx, &jobspb.UpdateJobStatusRequest{
			Id:     jobID,
			Status: statusFailed,
		}); _err != nil {
			return _err
		}

		// Increment the workflow failure count and check if the threshold is reached
		res, _err := r.svc.Workflows.IncrementWorkflowConsecutiveJobFailuresCount(ctx, &workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest{
			Id: workflowID,
		})
		if _err != nil {
			return _err
		}

		// If the threshold has been reached, terminate the workflow
		if res.GetThresholdReached() {
			if _, _err := r.svc.Workflows.TerminateWorkflow(ctx, &workflowspb.TerminateWorkflowRequest{
				Id:     workflowID,
				UserId: workflow.GetUserId(),
			}); _err != nil {
				return _err
			}
		}

		return err
	}

	// Update the job status from RUNNING to COMPLETED
	if _, _err := r.svc.Jobs.UpdateJobStatus(ctx, &jobspb.UpdateJobStatusRequest{
		Id:     jobID,
		Status: statusCompleted,
	}); _err != nil {
		return _err
	}

	// Reset the workflow consecutive job failures count
	if _, _err := r.svc.Workflows.ResetWorkflowConsecutiveJobFailuresCount(ctx, &workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest{
		Id: workflowID,
	}); _err != nil {
		return _err
	}

	return nil
}

// extractDataFromRecordValue extracts the data from the record value.
func extractDataFromRecordValue(recordValue string) (jobID, workflowID string, lastScheduledAt time.Time, err error) {
	parts := strings.Split(recordValue, string(delimiter))
	if len(parts) != 3 {
		return "", "", time.Time{}, status.Error(codes.InvalidArgument, "invalid data format")
	}

	lastScheduledAt, err = time.Parse(time.RFC3339Nano, parts[2])
	if err != nil {
		return "", "", time.Time{}, status.Error(codes.InvalidArgument, "invalid scheduled at time")
	}

	return parts[0], parts[1], lastScheduledAt, nil
}

// withAuthorization issues the necessary headers and tokens for authorization.
func (r *Repository) withAuthorization(ctx context.Context) (context.Context, error) {
	// Attach the audience and role to the context
	ctx = auth.WithAudience(ctx, svcpkg.Info().GetName())
	ctx = auth.WithRole(ctx, auth.RoleAdmin.String())

	// Issue a new token
	authToken, err := r.auth.IssueToken(ctx, authSubject)
	if err != nil {
		return nil, err
	}

	// Attach all the necessary headers and tokens to the context
	ctx = auth.WithAudienceInMetadata(ctx, svcpkg.Info().GetName())
	ctx = auth.WithRoleInMetadata(ctx, auth.RoleAdmin)
	ctx = auth.WithAuthorizationTokenInMetadata(ctx, authToken)

	return ctx, nil
}

// executeWorkflow executes the workflow.
func (r *Repository) executeWorkflow(ctx context.Context, jobID string, workflow *workflowspb.GetWorkflowByIDResponse) error {
	workflowID := workflow.GetId()
	userID := workflow.GetUserId()

	kind := workflow.GetKind()
	payload := workflow.GetPayload()

	switch kind {
	// Execute the HEARTBEAT workflow
	case workflowHeartBeat:
		return r.svc.Hsvc.Execute(ctx, payload)
	// Execute the CONTAINER workflow
	case workflowContainer:
		imageName, cmd, err := extractContainerDetails(payload)
		if err != nil {
			return err
		}

		logs, _, workflowErr := r.svc.Csvc.Execute(
			ctx,
			containerWorkflowExecutionTimeout,
			imageName,
			cmd,
		)

		var sequenceNum uint32

		// Publish the logs to the Kafka topic
		for log := range logs {
			currentSeq := atomic.AddUint32(&sequenceNum, 1)

			// Serialize the log entry
			logEntryBytes, err := json.Marshal(&jobsmodel.JobLogEntry{
				JobID:       jobID,
				WorkflowID:  workflowID,
				UserID:      userID,
				Message:     log,
				TimeStamp:   time.Now(),
				SequenceNum: currentSeq,
			})
			if err != nil {
				continue
			}

			if err := r.kfk.ProduceSync(ctx, kgo.SliceRecord(logEntryBytes)).FirstErr(); err != nil {
				continue
			}
		}

		return workflowErr
	default:
		return status.Error(codes.InvalidArgument, "invalid workflow kind")
	}
}

// extractContainerDetails extracts the container details from the workflow payload.
func extractContainerDetails(payload string) (imageName string, cmdStr []string, err error) {
	var data map[string]any
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		return "", nil, status.Error(codes.InvalidArgument, "invalid payload format")
	}

	imageName, ok := data["image"].(string)
	if !ok {
		return "", nil, status.Error(codes.InvalidArgument, "image is missing or invalid")
	}

	cmd, ok := data["cmd"].([]any)
	if !ok {
		return "", nil, status.Error(codes.InvalidArgument, "cmd is missing or invalid")
	}

	for _, c := range cmd {
		c, err := c.(string)
		if !err {
			return "", nil, status.Error(codes.InvalidArgument, "cmd is invalid")
		}

		cmdStr = append(cmdStr, c)
	}

	return imageName, cmdStr, nil
}
