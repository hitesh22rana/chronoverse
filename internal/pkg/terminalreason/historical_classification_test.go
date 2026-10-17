package terminalreason_test

import (
	"testing"

	"github.com/hitesh22rana/chronoverse/internal/pkg/terminalreason"
)

func TestResolveHistoricalFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name          string
		persistedCode string
		failureKind   string
		errorCode     string
		errorMessage  string
		wantCode      terminalreason.Code
		wantMessage   string
	}{
		{
			name:         "deadline exceeded with container timeout text",
			failureKind:  "USER",
			errorCode:    "DeadlineExceeded",
			errorMessage: "rpc error: code = DeadlineExceeded desc = container execution timed out: context deadline exceeded",
			wantCode:     terminalreason.TimeLimitExceeded,
			wantMessage:  "Execution time limit exceeded",
		},
		{
			name:         "container timeout text mixed case",
			failureKind:  "USER",
			errorCode:    "DeadlineExceeded",
			errorMessage: "Container Execution Timed Out: context deadline exceeded",
			wantCode:     terminalreason.TimeLimitExceeded,
			wantMessage:  "Execution time limit exceeded",
		},
		{
			// The gRPC deadline code alone is not a time limit; the historical text must confirm it.
			name:         "deadline code with exit text is not time limit",
			failureKind:  "USER",
			errorCode:    "DeadlineExceeded",
			errorMessage: "rpc error: code = DeadlineExceeded desc = container exited with non-zero code: 137",
			wantCode:     terminalreason.NonZeroExit,
			wantMessage:  "Process exited with non zero exit code",
		},
		{
			name:         "deadline code without container timeout text is unavailable",
			failureKind:  "USER",
			errorCode:    "DeadlineExceeded",
			errorMessage: "rpc error: code = DeadlineExceeded desc = context deadline exceeded",
			wantCode:     terminalreason.FailureReasonUnavailable,
			wantMessage:  "Failure reason unavailable",
		},
		{
			name:         "non zero exit text mixed case",
			failureKind:  "USER",
			errorCode:    "Aborted",
			errorMessage: "Container Exited With Non-Zero Code: 2",
			wantCode:     terminalreason.NonZeroExit,
			wantMessage:  "Process exited with non zero exit code",
		},
		{
			name:         "failed to pull image text",
			failureKind:  "USER",
			errorCode:    "NotFound",
			errorMessage: "rpc error: code = NotFound desc = failed to pull image: pull access denied for registry.local/app",
			wantCode:     terminalreason.ImagePullFailed,
			wantMessage:  "Container image could not be pulled",
		},
		{
			name:         "image pull output text",
			failureKind:  "USER",
			errorCode:    "Aborted",
			errorMessage: "rpc error: code = Aborted desc = failed to read image pull output: unexpected EOF",
			wantCode:     terminalreason.ImagePullFailed,
			wantMessage:  "Container image could not be pulled",
		},
		{
			name:         "image pull text mixed case",
			failureKind:  "USER",
			errorCode:    "NotFound",
			errorMessage: "FAILED TO PULL IMAGE: manifest unknown",
			wantCode:     terminalreason.ImagePullFailed,
			wantMessage:  "Container image could not be pulled",
		},
		{
			name:         "unexpected status code text",
			failureKind:  "USER",
			errorCode:    "FailedPrecondition",
			errorMessage: "rpc error: code = FailedPrecondition desc = unexpected status code: got 500, want 200",
			wantCode:     terminalreason.UnexpectedStatusCode,
			wantMessage:  "Endpoint returned an unexpected status code",
		},
		{
			name:         "unexpected status code text mixed case",
			failureKind:  "USER",
			errorCode:    "FailedPrecondition",
			errorMessage: "Unexpected Status Code: got 503, want 200",
			wantCode:     terminalreason.UnexpectedStatusCode,
			wantMessage:  "Endpoint returned an unexpected status code",
		},
		{
			name:         "system kind fallback",
			failureKind:  "SYSTEM",
			errorCode:    "Unknown",
			errorMessage: "worker terminated without a classification marker",
			wantCode:     terminalreason.SystemError,
			wantMessage:  "Job failed because of a system error",
		},
		{
			// Recognized workload text outranks the broad SYSTEM kind.
			name:         "specific exit text outranks system kind",
			failureKind:  "SYSTEM",
			errorCode:    "Aborted",
			errorMessage: "container exited with non-zero code: 1",
			wantCode:     terminalreason.NonZeroExit,
			wantMessage:  "Process exited with non zero exit code",
		},
		{
			name:         "unmatched user history is unavailable",
			failureKind:  "USER",
			errorCode:    "Unknown",
			errorMessage: "job failed",
			wantCode:     terminalreason.FailureReasonUnavailable,
			wantMessage:  "Failure reason unavailable",
		},
		{
			name:        "empty history is unavailable",
			failureKind: "USER",
			wantCode:    terminalreason.FailureReasonUnavailable,
			wantMessage: "Failure reason unavailable",
		},
		{
			name:         "timeout text outranks exit text",
			failureKind:  "USER",
			errorCode:    "DeadlineExceeded",
			errorMessage: "container execution timed out; container exited with non-zero code: 143",
			wantCode:     terminalreason.TimeLimitExceeded,
			wantMessage:  "Execution time limit exceeded",
		},
		{
			name:         "exit text outranks image pull text",
			failureKind:  "USER",
			errorCode:    "Aborted",
			errorMessage: "container exited with non-zero code: 1 after failed to pull image",
			wantCode:     terminalreason.NonZeroExit,
			wantMessage:  "Process exited with non zero exit code",
		},
		{
			name:         "image pull text outranks status text",
			failureKind:  "USER",
			errorCode:    "NotFound",
			errorMessage: "failed to pull image: unexpected status code 403",
			wantCode:     terminalreason.ImagePullFailed,
			wantMessage:  "Container image could not be pulled",
		},
		{
			name:          "persisted failure code wins over historical timeout",
			persistedCode: terminalreason.NonZeroExit.String(),
			failureKind:   "USER",
			errorCode:     "DeadlineExceeded",
			errorMessage:  "container execution timed out: context deadline exceeded",
			wantCode:      terminalreason.NonZeroExit,
			wantMessage:   "Process exited with non zero exit code",
		},
		{
			name:          "persisted image pull code wins over system fallback",
			persistedCode: terminalreason.ImagePullFailed.String(),
			failureKind:   "SYSTEM",
			errorCode:     "Unknown",
			errorMessage:  "worker terminated without a classification marker",
			wantCode:      terminalreason.ImagePullFailed,
			wantMessage:   "Container image could not be pulled",
		},
		{
			// Cancellation codes are not a failure family member, so failed history is derived.
			name:          "cancellation code is not honored for failed status",
			persistedCode: terminalreason.WorkflowTerminated.String(),
			failureKind:   "SYSTEM",
			errorCode:     "Unknown",
			errorMessage:  "worker terminated without a classification marker",
			wantCode:      terminalreason.SystemError,
			wantMessage:   "Job failed because of a system error",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, message, ok := terminalreason.Resolve("FAILED", tc.persistedCode, tc.failureKind, tc.errorCode, tc.errorMessage)
			if !ok || code != tc.wantCode || message != tc.wantMessage {
				t.Fatalf("Resolve() = %q, %q, %v; want %q, %q, true", code, message, ok, tc.wantCode, tc.wantMessage)
			}
		})
	}
}
