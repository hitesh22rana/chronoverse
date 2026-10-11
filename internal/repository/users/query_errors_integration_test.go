//nolint:testpackage // Verifies repository errors and their recorded spans.
package users

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	otelcodes "go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIntegrationUserContextErrorsAreTraced(t *testing.T) {
	operations := []struct {
		name string
		call func(context.Context, *Repository) error
	}{
		{"LoginUser", func(ctx context.Context, repo *Repository) error {
			user, token, err := repo.LoginUser(ctx, "canceled@chronoverse.test", "password")
			if user != nil || token != "" {
				t.Error("failed login returned authentication material")
			}
			return err
		}},
		{"GetUser", func(ctx context.Context, repo *Repository) error {
			user, err := repo.GetUser(ctx, uuid.NewString())
			if user != nil {
				t.Error("failed lookup returned a user")
			}
			return err
		}},
		{"UpdateUser", func(ctx context.Context, repo *Repository) error {
			return repo.UpdateUser(ctx, uuid.NewString(), "ALERTS")
		}},
	}
	for _, operation := range operations {
		for _, code := range []codes.Code{codes.Canceled, codes.DeadlineExceeded} {
			t.Run(operation.name+"/"+code.String(), func(t *testing.T) {
				repo := newTestRepository(t)
				recorder := tracetest.NewSpanRecorder()
				provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
				t.Cleanup(func() {
					if err := provider.Shutdown(context.Background()); err != nil {
						t.Error(err)
					}
				})
				repo.tp = provider.Tracer("user-query-errors")
				ctx, cancel := context.WithCancel(context.Background())
				if code == codes.DeadlineExceeded {
					cancel()
					ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				}
				cancel()
				err := operation.call(ctx, repo)
				if status.Code(err) != code {
					t.Fatalf("error = %v, want %s", err, code)
				}
				assertUserErrorSpan(t, recorder, operation.name, err)
			})
		}
	}
}

func assertUserErrorSpan(t *testing.T, recorder *tracetest.SpanRecorder, operation string, err error) {
	t.Helper()
	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(spans))
	}
	span := spans[0]
	if span.Name() != "Repository."+operation || span.Status().Code != otelcodes.Error || span.Status().Description != err.Error() {
		t.Fatalf("span = %q, %v; want returned error %v", span.Name(), span.Status(), err)
	}
	events := span.Events()
	if len(events) != 1 || events[0].Name != "exception" {
		t.Fatalf("span events = %v, want one recorded exception", events)
	}
	for _, attribute := range events[0].Attributes {
		if attribute.Key == "exception.message" {
			if attribute.Value.AsString() != err.Error() {
				t.Fatalf("recorded error = %q, want %q", attribute.Value.AsString(), err.Error())
			}
			return
		}
	}
	t.Fatal("recorded exception has no message")
}
