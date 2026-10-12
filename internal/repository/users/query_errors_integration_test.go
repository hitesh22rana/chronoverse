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
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	usersmodel "github.com/hitesh22rana/chronoverse/internal/model/users"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
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

//nolint:gocyclo // Keeps interruption, tracing, cleanup, and retry beside each read.
func TestIntegrationUserReadsInterruptRowConsumption(t *testing.T) {
	operations := []struct {
		name, pattern string
		call          func(context.Context, *Repository, string, string) (*usersmodel.GetUserResponse, string, error)
	}{
		{"LoginUser", "%FROM users WHERE email%", func(ctx context.Context, repo *Repository, _, email string) (*usersmodel.GetUserResponse, string, error) {
			return repo.LoginUser(ctx, email, "password")
		}},
		{"GetUser", "%FROM users WHERE id%", func(ctx context.Context, repo *Repository, id, _ string) (*usersmodel.GetUserResponse, string, error) {
			user, err := repo.GetUser(ctx, id)
			return user, "", err
		}},
	}
	for _, operation := range operations {
		for _, code := range []codes.Code{codes.Canceled, codes.DeadlineExceeded} {
			t.Run(operation.name+"/"+code.String(), func(t *testing.T) {
				ctx := context.Background()
				pg := testkit.Postgres(t)
				email := "interrupted-" + uuid.NewString() + "@chronoverse.test"
				registered, _, err := newTestRepository(t).RegisterUser(ctx, email, "password", "register-"+uuid.NewString())
				if err != nil {
					t.Fatal(err)
				}
				issuer := authmock.NewMockIAuth(gomock.NewController(t))
				repo, recorder := newTracedRepository(t, issuer, pg)
				remove := installUsersReadFault(t, pg, "PERFORM pg_sleep(30); RETURN now();")
				commandCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				done := make(chan error, 1)
				finished := make(chan struct{})
				go func() {
					defer close(finished)
					user, token, callErr := operation.call(commandCtx, repo, registered.ID, email)
					if user != nil || token != "" {
						t.Error("interrupted read returned a user or authentication material")
					}
					done <- callErr
				}()
				defer func() {
					cancel()
					select {
					case <-finished:
					case <-time.After(5 * time.Second):
						t.Error("interrupted read did not stop during cleanup")
					}
				}()
				waitForSleepingUserQuery(ctx, t, pg, operation.pattern)
				if code == codes.Canceled {
					cancel()
				}
				select {
				case err = <-done:
				case <-time.After(10 * time.Second):
					t.Fatal("read did not return after interruption")
				}
				if status.Code(err) != code {
					t.Fatalf("error = %v, want %s", err, code)
				}
				assertUserErrorSpan(t, recorder, operation.name, err)
				remove()
				if operation.name == "LoginUser" {
					issuer.EXPECT().IssueToken(gomock.Any(), registered.ID, auth.ServiceNameServer).Return("retry-token", nil)
				}
				user, _, err := operation.call(ctx, repo, registered.ID, email)
				if err != nil || user == nil || user.ID != registered.ID {
					t.Fatalf("read after restoring table = %v, %v", user, err)
				}
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
