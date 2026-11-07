//nolint:testpackage // Exercises header transport failures at the app boundary.
package users

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	otelcodes "go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	mocks "github.com/hitesh22rana/chronoverse/internal/app/users/mock"
	pb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"
)

type failedIdentityHeaders struct {
	method  string
	headers metadata.MD
	calls   int
}

func (s *failedIdentityHeaders) Method() string            { return s.method }
func (*failedIdentityHeaders) SetHeader(metadata.MD) error { return nil }
func (s *failedIdentityHeaders) SendHeader(headers metadata.MD) error {
	s.calls++
	s.headers = headers.Copy()
	return io.ErrClosedPipe
}
func (*failedIdentityHeaders) SetTrailer(metadata.MD) error { return nil }

func TestIdentityHeaderFailureReturnsNoResponseAndRecordsTraceError(t *testing.T) {
	t.Parallel()
	for _, login := range []bool{false, true} {
		ctrl := gomock.NewController(t)
		service := mocks.NewMockService(ctrl)
		recorder := tracetest.NewSpanRecorder()
		provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
			defer cancel()
			require.NoError(t, provider.Shutdown(ctx))
		})
		app := &Users{tp: provider.Tracer("header-contract"), cfg: &Config{Deadline: time.Second}, svc: service}
		transport := &failedIdentityHeaders{method: "/users.UsersService/RegisterUser"}
		if login {
			transport.method = "/users.UsersService/LoginUser"
		}
		ctx := grpc.NewContextWithServerTransportStream(t.Context(), transport)
		var response proto.Message
		var err error
		if login {
			// #nosec G101 -- Public password fixture, never a deployed credential.
			request := &pb.LoginUserRequest{Email: "person@example.com", Password: "9gR!bQ7@zL2#vN5$"}
			service.EXPECT().LoginUser(gomock.Any(), request).Return("550e8400-e29b-41d4-a716-446655440000", "private-token", nil)
			response, err = app.LoginUser(ctx, request)
		} else {
			// #nosec G101 -- Public password fixture, never a deployed credential.
			request := &pb.RegisterUserRequest{Email: "person@example.com", Password: "9gR!bQ7@zL2#vN5$", IdempotencyKey: "registration"}
			service.EXPECT().RegisterUser(gomock.Any(), request).Return("550e8400-e29b-41d4-a716-446655440000", "private-token", nil)
			response, err = app.RegisterUser(ctx, request)
		}
		require.Equal(t, codes.Unknown, status.Code(err))
		require.Equal(t, io.ErrClosedPipe.Error(), status.Convert(err).Message())
		require.Nil(t, response)
		require.Equal(t, 1, transport.calls)
		require.Equal(t, []string{"Bearer private-token"}, transport.headers.Get("authorization"))
		spans := recorder.Ended()
		require.Len(t, spans, 1)
		require.Equal(t, otelcodes.Error, spans[0].Status().Code)
		require.Equal(t, err.Error(), spans[0].Status().Description)
		require.Len(t, spans[0].Events(), 1)
		attributes := fmt.Sprint(spans[0].Attributes())
		for _, secret := range []string{"person@example.com", "9gR!bQ7@zL2#vN5$", "private-token"} {
			require.NotContains(t, attributes, secret)
		}
	}
}
