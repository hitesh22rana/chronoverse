package terminalreason

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Code is a stable, user-safe explanation for a terminal job status.
type Code string

// String returns the wire representation of the reason code.
func (c Code) String() string { return string(c) }

const (
	// TimeLimitExceeded indicates a configured execution or request deadline elapsed.
	TimeLimitExceeded Code = "TIME_LIMIT_EXCEEDED"
	// NonZeroExit indicates a workload process exited unsuccessfully.
	NonZeroExit Code = "NON_ZERO_EXIT"
	// ImagePullFailed indicates the workload image could not be pulled.
	ImagePullFailed Code = "IMAGE_PULL_FAILED"
	// UnexpectedStatusCode indicates a heartbeat returned a status other than expected.
	UnexpectedStatusCode Code = "UNEXPECTED_STATUS_CODE"
	// ExecutionFailed is the safe fallback for current user-workload failures.
	ExecutionFailed Code = "EXECUTION_FAILED"
	// SystemError is the safe fallback for infrastructure failures.
	SystemError Code = "SYSTEM_ERROR"
	// FailureReasonUnavailable indicates a historical failure cannot be classified safely.
	FailureReasonUnavailable Code = "FAILURE_REASON_UNAVAILABLE"
	// WorkflowTerminated indicates cancellation caused by workflow termination.
	WorkflowTerminated Code = "WORKFLOW_TERMINATED"
	// WorkflowUpdated indicates cancellation caused by a workflow change.
	WorkflowUpdated Code = "WORKFLOW_UPDATED"
	// CancellationReasonUnavailable indicates a historical cancellation has no reason.
	CancellationReasonUnavailable Code = "CANCELLATION_REASON_UNAVAILABLE"
)

var messages = map[Code]string{
	TimeLimitExceeded:             "Execution time limit exceeded",
	NonZeroExit:                   "Process exited with non zero exit code",
	ImagePullFailed:               "Container image could not be pulled",
	UnexpectedStatusCode:          "Endpoint returned an unexpected status code",
	ExecutionFailed:               "Job execution failed",
	SystemError:                   "Job failed because of a system error",
	FailureReasonUnavailable:      "Failure reason unavailable",
	WorkflowTerminated:            "Canceled because the workflow was terminated",
	WorkflowUpdated:               "Canceled because the workflow was updated",
	CancellationReasonUnavailable: "Cancellation reason unavailable",
}

var failureCodes = map[Code]struct{}{
	TimeLimitExceeded: {}, NonZeroExit: {}, ImagePullFailed: {}, UnexpectedStatusCode: {},
	ExecutionFailed: {}, SystemError: {}, FailureReasonUnavailable: {},
}

var cancellationCodes = map[Code]struct{}{
	WorkflowTerminated: {}, WorkflowUpdated: {}, CancellationReasonUnavailable: {},
}

// Message returns the canonical safe message for code.
func Message(code Code) (string, bool) {
	message, ok := messages[code]
	return message, ok
}

// ValidateFailure validates that value belongs to the failure reason family.
func ValidateFailure(value string) error {
	if _, ok := failureCodes[Code(value)]; !ok {
		return fmt.Errorf("invalid failure reason code %q", value)
	}
	return nil
}

// ValidateCancellation validates that value belongs to the cancellation reason family.
func ValidateCancellation(value string) error {
	if _, ok := cancellationCodes[Code(value)]; !ok {
		return fmt.Errorf("invalid cancellation reason code %q", value)
	}
	return nil
}

// Error carries a terminal reason while retaining the wrapped error's behavior.
type Error struct {
	reason Code
	err    error
}

func (e *Error) Error() string { return e.err.Error() }

// Unwrap preserves errors.Is/errors.As traversal through execution layers.
func (e *Error) Unwrap() error { return e.err }

// Is preserves context identity for gRPC status errors that represent context termination.
func (e *Error) Is(target error) bool {
	if errors.Is(e.err, target) {
		return true
	}
	switch target {
	case context.Canceled:
		return status.Code(e.err) == codes.Canceled
	case context.DeadlineExceeded:
		return status.Code(e.err) == codes.DeadlineExceeded
	default:
		return false
	}
}

// GRPCStatus preserves the wrapped gRPC or context status.
func (e *Error) GRPCStatus() *status.Status {
	if errors.Is(e.err, context.Canceled) || errors.Is(e.err, context.DeadlineExceeded) {
		return status.FromContextError(e.err)
	}
	return status.Convert(e.err)
}

// Wrap annotates err with a terminal reason.
func Wrap(code Code, err error) error {
	if err == nil {
		return nil
	}
	return &Error{reason: code, err: err}
}

// FromError extracts the first structured terminal reason in an error chain.
func FromError(err error) (Code, bool) {
	var reasonErr *Error
	if !errors.As(err, &reasonErr) {
		return "", false
	}
	return reasonErr.reason, true
}

// Resolve returns the safe terminal reason fields for a job response.
func Resolve(jobStatus, persistedCode, failureKind, errorCode, errorMessage string) (Code, string, bool) {
	switch jobStatus {
	case "FAILED":
		code := Code(persistedCode)
		if _, ok := failureCodes[code]; !ok {
			code = deriveHistoricalFailure(failureKind, errorCode, errorMessage)
		}
		message, _ := Message(code)
		return code, message, true
	case "CANCELED":
		code := Code(persistedCode)
		if _, ok := cancellationCodes[code]; !ok {
			code = CancellationReasonUnavailable
		}
		message, _ := Message(code)
		return code, message, true
	default:
		return "", "", false
	}
}

func deriveHistoricalFailure(failureKind, errorCode, errorMessage string) Code {
	message := strings.ToLower(errorMessage)
	switch {
	case errorCode == "DeadlineExceeded" && strings.Contains(message, "container execution timed out"):
		return TimeLimitExceeded
	case strings.Contains(message, "container exited with non-zero code"):
		return NonZeroExit
	case strings.Contains(message, "failed to pull image") || strings.Contains(message, "image pull output"):
		return ImagePullFailed
	case strings.Contains(message, "unexpected status code"):
		return UnexpectedStatusCode
	case failureKind == "SYSTEM":
		return SystemError
	default:
		return FailureReasonUnavailable
	}
}
