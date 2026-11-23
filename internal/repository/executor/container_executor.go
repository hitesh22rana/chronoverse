package executor

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/joblogevents"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

// executeContainerWorkflow executes the CONTAINER workflow.
func (r *Repository) executeContainerWorkflow(
	ctx context.Context,
	csvc ContainerSvc,
	jobID,
	leaseToken string,
	runtimeNodeID string,
	attempts int32,
	workflow *workflowspb.GetWorkflowByIDResponse,
) (string, error) {
	details, err := container.ExtractAndValidateContainerDetails(workflow.GetPayload())
	if err != nil {
		return "", err
	}

	image := workflow.GetResolvedImageDigest()
	if image == "" {
		image = details.Image
	}

	if err := csvc.Build(ctx, image); err != nil {
		return "", err
	}

	return r.executeContainerIntent(ctx, csvc, jobID, leaseToken, runtimeNodeID, attempts, workflow, container.CreateRequest{
		Image: image, Cmd: append([]string(nil), details.Cmd...), Env: append([]string(nil), details.Env...), Timeout: details.TimeOut,
	})
}

func (r *Repository) attachJobContainer(ctx context.Context, jobID, leaseToken, containerID, runtimeNodeID string) error {
	ctx, cancel := context.WithTimeout(ctx, containerControlTimeout)
	defer cancel()

	jobCtx, err := r.withAuthorization(ctx)
	if err != nil {
		return err
	}

	_, err = r.svc.Jobs.AttachJobContainer(jobCtx, &jobspb.AttachJobContainerRequest{
		Id:            jobID,
		LeaseToken:    leaseToken,
		ContainerId:   containerID,
		RuntimeNodeId: runtimeNodeID,
		CommandId:     uuid.NewString(),
	})
	if ctx.Err() != nil {
		return status.FromContextError(ctx.Err()).Err()
	}

	return err
}

func (r *Repository) processContainerExecution(
	ctx context.Context,
	jobID string,
	attempts int32,
	workflow *workflowspb.GetWorkflowByIDResponse,
	logs <-chan *jobsmodel.JobLog,
	errs <-chan error,
) error {
	done := make(chan struct{})
	defer close(done)

	logDone := r.publishContainerLogs(ctx, jobID, attempts, workflow, logs, done)
	execErrors := errs
	var execErr error
	var secondary strings.Builder
	for execErrors != nil || logDone != nil {
		select {
		case err, ok := <-execErrors:
			if !ok {
				execErrors = nil
				continue
			}
			if err == nil {
				continue
			}
			if execErr == nil {
				execErr = err
				continue
			}
			remaining := maxSecondaryDiagnosticBytes - secondary.Len()
			if remaining > 1 {
				if secondary.Len() > 0 {
					secondary.WriteByte('\n')
					remaining--
				}
				secondary.WriteString(renderExecutionDiagnostic(err.Error(), remaining))
			}
		case <-logDone:
			logDone = nil
		}
	}

	return withExecutionDiagnostics(execErr, secondary.String())
}

const (
	// Leave headroom below job storage and intent RPC limits for status/context wrapping.
	maxExecutionDiagnosticBytes = 1536
	maxSecondaryDiagnosticBytes = 512
)

// Only the primary participates in errors.Is/As and retry classification; later
// errors are diagnostic data, never additional workload reasons or contexts.
type executionDiagnosticError struct {
	primary error
	message string
}

func (e *executionDiagnosticError) Error() string { return e.message }
func (e *executionDiagnosticError) Unwrap() error { return e.primary }
func (e *executionDiagnosticError) GRPCStatus() *status.Status {
	code := status.Code(e.primary)
	if code == codes.Unknown {
		code = status.FromContextError(e.primary).Code()
	}
	return status.New(code, e.message)
}

func withExecutionDiagnostics(primary error, secondary string) error {
	if primary == nil {
		return nil
	}
	suffix := ""
	if secondary != "" {
		suffix = "\nadditional execution diagnostics: " + secondary
	}
	message := renderExecutionDiagnostic(primary.Error(), maxExecutionDiagnosticBytes-len(suffix)) + suffix
	if secondary == "" && message == primary.Error() &&
		renderExecutionDiagnostic(status.Convert(primary).Message(), maxExecutionDiagnosticBytes) == status.Convert(primary).Message() {
		return primary
	}
	return &executionDiagnosticError{primary: primary, message: message}
}

// Render before protobuf/SQL: valid UTF-8 can still contain NUL, which PostgreSQL
// text rejects. Bound while decoding rather than building an unbounded aggregate.
func renderExecutionDiagnostic(text string, limit int) string {
	var rendered strings.Builder
	for text != "" {
		r, size := utf8.DecodeRuneInString(text)
		token := string(r)
		if r == 0 {
			token = `\x00`
		}
		if rendered.Len()+len(token) > limit {
			const marker = "…"
			if limit < len(marker) {
				return strings.Repeat(".", limit)
			}
			prefix := rendered.String()
			prefix = prefix[:min(len(prefix), limit-len(marker))]
			for !utf8.ValidString(prefix) {
				prefix = prefix[:len(prefix)-1]
			}
			return prefix + marker
		}
		rendered.WriteString(token)
		text = text[size:]
	}
	return rendered.String()
}

func (r *Repository) publishContainerLogs(
	ctx context.Context,
	jobID string,
	attempts int32,
	workflow *workflowspb.GetWorkflowByIDResponse,
	logs <-chan *jobsmodel.JobLog,
	done <-chan struct{},
) <-chan struct{} {
	logDone := make(chan struct{})
	go func() {
		defer close(logDone)

		batchSize := r.cfg.JobLogBatchSize
		records := make([]*kgo.Record, 0, batchSize)
		ticker := time.NewTicker(r.cfg.JobLogBatchInterval)
		defer ticker.Stop()

		flush := func() {
			if len(records) == 0 {
				return
			}

			r.publishJobLogBatch(ctx, records)
			records = make([]*kgo.Record, 0, batchSize)
		}

		for {
			select {
			case log, ok := <-logs:
				if !ok {
					flush()
					return
				}
				record := r.jobLogRecord(ctx, jobID, attempts, workflow, log)
				if record == nil {
					continue
				}
				records = append(records, record)
				if len(records) >= batchSize {
					flush()
				}
			case <-ticker.C:
				flush()
			case <-done:
				flush()
				return
			}
		}
	}()

	return logDone
}

func (r *Repository) jobLogRecord(
	ctx context.Context,
	jobID string,
	attempts int32,
	workflow *workflowspb.GetWorkflowByIDResponse,
	log *jobsmodel.JobLog,
) *kgo.Record {
	event := &jobsmodel.JobLogEvent{
		EventKey:    idempotency.LogEventKey(jobID, log.Stream, log.SequenceNum, attempts),
		JobID:       jobID,
		WorkflowID:  workflow.GetId(),
		UserID:      workflow.GetUserId(),
		Message:     log.Message,
		TimeStamp:   log.Timestamp,
		SequenceNum: log.SequenceNum,
		Stream:      log.Stream,
		Retention:   workflow.GetLogRetention(),
	}

	r.publishLiveJobLog(ctx, event)

	record, err := joblogevents.KafkaRecord(event)
	if err != nil {
		loggerpkg.FromContext(ctx).Error("failed to create job log kafka record",
			zap.String("job_id", event.JobID),
			zap.String("workflow_id", event.WorkflowID),
			zap.String("event_key", event.EventKey),
			zap.String("stream", event.Stream),
			zap.Uint32("sequence_num", event.SequenceNum),
			zap.Error(err),
		)
		return nil
	}

	return record
}

func contextError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, err.Error())
	}

	return status.Error(codes.Canceled, err.Error())
}
