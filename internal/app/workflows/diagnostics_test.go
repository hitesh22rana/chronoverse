//nolint:testpackage // Verifies telemetry emitted by the RPC boundary.
package workflows

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/mock/gomock"

	mocks "github.com/hitesh22rana/chronoverse/internal/app/workflows/mock"
	pb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

func TestPayloadIsForwardedWithoutDiagnosticDisclosure(t *testing.T) {
	const payload = `{"headers":{"Authorization":["private-header"]},"env":{"TOKEN":"private-env"}}`
	t.Run("CreateWorkflow", func(t *testing.T) {
		recorder := tracetest.NewSpanRecorder()
		provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
		t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.WithoutCancel(t.Context()))) })
		service := mocks.NewMockService(gomock.NewController(t))
		app := &Workflows{tp: provider.Tracer("diagnostic-contract"), cfg: &Config{Deadline: time.Second}, svc: service}
		request := &pb.CreateWorkflowRequest{UserId: "user-1", Payload: payload}
		service.EXPECT().CreateWorkflow(gomock.Any(), request).Return("resource-1", nil)
		_, err := app.CreateWorkflow(t.Context(), request)
		require.NoError(t, err)
		require.Equal(t, payload, request.GetPayload())
		spans := recorder.Ended()
		require.Len(t, spans, 1)
		diagnostic := fmt.Sprint(spans[0].Attributes(), spans[0].Events(), spans[0].Status())
		require.NotContains(t, diagnostic, "private-header")
		require.NotContains(t, diagnostic, "private-env")
		require.Contains(t, diagnostic, "user-1")
	})
	t.Run("UpdateWorkflow", func(t *testing.T) {
		recorder := tracetest.NewSpanRecorder()
		provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
		t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.WithoutCancel(t.Context()))) })
		service := mocks.NewMockService(gomock.NewController(t))
		app := &Workflows{tp: provider.Tracer("diagnostic-contract"), cfg: &Config{Deadline: time.Second}, svc: service}
		request := &pb.UpdateWorkflowRequest{UserId: "user-1", Payload: payload}
		service.EXPECT().UpdateWorkflow(gomock.Any(), request).Return(nil)
		_, err := app.UpdateWorkflow(t.Context(), request)
		require.NoError(t, err)
		require.Equal(t, payload, request.GetPayload())
		spans := recorder.Ended()
		require.Len(t, spans, 1)
		diagnostic := fmt.Sprint(spans[0].Attributes(), spans[0].Events(), spans[0].Status())
		require.NotContains(t, diagnostic, "private-header")
		require.NotContains(t, diagnostic, "private-env")
		require.Contains(t, diagnostic, "user-1")
	})
}
