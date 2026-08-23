//nolint:testpackage // Verifies private telemetry emission without widening the API.
package commandidempotency

import (
	"context"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestRecordOutcomeEmitsOnlyPublishedOperationAndClassification(t *testing.T) {
	t.Parallel()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	ctx, span := provider.Tracer("test").Start(context.Background(), "command")
	recordOutcome(ctx, OperationWorkflowCreate, "replay")
	span.End()

	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(ended))
	}
	attributes := ended[0].Attributes()
	if len(attributes) != 2 {
		t.Fatalf("idempotency attributes = %v, want only operation and outcome", attributes)
	}
	got := map[string]string{}
	for _, attr := range attributes {
		got[string(attr.Key)] = attr.Value.AsString()
	}
	if got["chronoverse.idempotency.operation"] != OperationWorkflowCreate {
		t.Fatalf("operation attribute = %q, want %q", got["chronoverse.idempotency.operation"], OperationWorkflowCreate)
	}
	if got["chronoverse.idempotency.outcome"] != "replay" {
		t.Fatalf("outcome attribute = %q, want replay", got["chronoverse.idempotency.outcome"])
	}
}
