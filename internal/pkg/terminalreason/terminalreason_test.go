package terminalreason_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/terminalreason"
)

func TestReasonFamilies(t *testing.T) {
	if err := terminalreason.ValidateFailure(terminalreason.TimeLimitExceeded.String()); err != nil {
		t.Fatal(err)
	}
	if err := terminalreason.ValidateCancellation(terminalreason.WorkflowTerminated.String()); err != nil {
		t.Fatal(err)
	}
	if err := terminalreason.ValidateFailure(terminalreason.WorkflowUpdated.String()); err == nil {
		t.Fatal("cancellation reason accepted as failure reason")
	}
	if err := terminalreason.ValidateCancellation(terminalreason.NonZeroExit.String()); err == nil {
		t.Fatal("failure reason accepted as cancellation reason")
	}
}

func TestWrappedErrorContract(t *testing.T) {
	cause := context.DeadlineExceeded
	err := terminalreason.Wrap(terminalreason.TimeLimitExceeded, cause)
	err = fmt.Errorf("outer: %w", err)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("wrapped context cause is not discoverable")
	}
	if got := status.Code(err); got != codes.DeadlineExceeded {
		t.Fatalf("status.Code() = %v", got)
	}
	if _, ok := status.FromError(err); !ok {
		t.Fatal("status.FromError() did not recognize wrapped status")
	}
	if got, ok := terminalreason.FromError(err); !ok || got != terminalreason.TimeLimitExceeded {
		t.Fatalf("FromError() = %q, %v", got, ok)
	}
}

func TestWrappedGRPCContextIdentity(t *testing.T) {
	err := terminalreason.Wrap(terminalreason.TimeLimitExceeded, status.Error(codes.DeadlineExceeded, "configured timeout"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("deadline gRPC status does not preserve context identity")
	}
	err = terminalreason.Wrap(terminalreason.SystemError, status.Error(codes.Canceled, "worker canceled"))
	if !errors.Is(err, context.Canceled) {
		t.Fatal("canceled gRPC status does not preserve context identity")
	}
}

func TestResolveFallbacks(t *testing.T) {
	code, message, ok := terminalreason.Resolve("FAILED", "UNKNOWN", "USER", "Unknown", "ambiguous")
	if !ok || code != terminalreason.FailureReasonUnavailable || message != "Failure reason unavailable" {
		t.Fatalf("Resolve failed fallback = %q, %q, %v", code, message, ok)
	}
	code, message, ok = terminalreason.Resolve("CANCELED", "", "", "", "")
	if !ok || code != terminalreason.CancellationReasonUnavailable || message != "Cancellation reason unavailable" {
		t.Fatalf("Resolve cancellation fallback = %q, %q, %v", code, message, ok)
	}
}
