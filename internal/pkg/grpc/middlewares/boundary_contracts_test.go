//nolint:testpackage // Checks the logging adapter's field and severity contracts.
package middlewares

import (
	"context"
	"errors"
	"testing"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
)

type contractTransport struct{ method string }

func (s contractTransport) Method() string             { return s.method }
func (contractTransport) SetHeader(metadata.MD) error  { return nil }
func (contractTransport) SendHeader(metadata.MD) error { return nil }
func (contractTransport) SetTrailer(metadata.MD) error { return nil }

//nolint:containedctx // A gRPC stream exposes its request context.
type contractStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s contractStream) Context() context.Context { return s.ctx }

//nolint:gocyclo // One matrix verifies unary and streaming policy parity.
func TestAuthorizationRequiresValidatedContextAndPropagatesHandlerErrors(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("handler failed")
	for _, health := range []bool{false, true} {
		method := "/jobs.JobsService/StreamJobLogs"
		if health {
			method = "/grpc.health.v1.Health/Watch"
		}
		for _, validated := range []bool{false, true} {
			ctx := metadata.NewIncomingContext(t.Context(), metadata.Pairs("role", "admin", "audience", "jobs-service"))
			if validated {
				ctx = auth.WithRole(auth.WithAudience(ctx, "jobs-service"), "user")
			}
			for _, deny := range []bool{false, true} {
				roleCalls := 0
				handlerCalls := 0
				role := func(gotMethod, gotRole string) bool {
					roleCalls++
					require.Equal(t, method, gotMethod)
					require.Equal(t, "user", gotRole)
					return deny
				}
				h := func(got context.Context, request any) (any, error) {
					handlerCalls++
					require.Equal(t, ctx, got)
					require.Equal(t, "request", request)
					return request, sentinel
				}
				_, err := UnaryAudienceInterceptor()(ctx, "request", &grpc.UnaryServerInfo{FullMethod: method}, func(c context.Context, r any) (any, error) {
					return UnaryRoleInterceptor(role)(c, r, &grpc.UnaryServerInfo{FullMethod: method}, h)
				})
				expected := codes.FailedPrecondition
				switch {
				case !validated && health:
					require.ErrorIs(t, err, sentinel)
				case validated && deny:
					expected = codes.PermissionDenied
					require.Equal(t, expected, status.Code(err))
				case validated:
					require.ErrorIs(t, err, sentinel)
				default:
					require.Equal(t, expected, status.Code(err))
				}
				wantHandler := 0
				if (!validated && health) || (validated && !deny) {
					wantHandler = 1
				}
				require.Equal(t, wantHandler, handlerCalls)
				wantRole := 0
				if validated {
					wantRole = 1
				}
				require.Equal(t, wantRole, roleCalls)
				roleCalls = 0
				handlerCalls = 0
				err = StreamAudienceInterceptor()(nil, contractStream{ctx: ctx}, &grpc.StreamServerInfo{FullMethod: method}, func(srv any, stream grpc.ServerStream) error {
					//nolint:contextcheck // The stream carries the validated request context.
					return StreamRoleInterceptor(role)(srv, stream, &grpc.StreamServerInfo{FullMethod: method}, func(_ any, wrapped grpc.ServerStream) error {
						handlerCalls++
						require.Equal(t, ctx, wrapped.Context())
						return sentinel
					})
				})
				switch {
				case !validated && health:
					require.ErrorIs(t, err, sentinel)
				case validated && deny:
					require.Equal(t, codes.PermissionDenied, status.Code(err))
				case validated:
					require.ErrorIs(t, err, sentinel)
				default:
					require.Equal(t, codes.FailedPrecondition, status.Code(err))
				}
				require.Equal(t, wantHandler, handlerCalls)
				require.Equal(t, wantRole, roleCalls)
			}
		}
	}
}

func TestLoggerPreservesFieldsAndSeverity(t *testing.T) {
	t.Parallel()
	core, entries := observer.New(zapcore.DebugLevel)
	logger := loggingInterceptor(zap.New(core))
	for _, tc := range []struct {
		level logging.Level
		want  zapcore.Level
	}{
		{
			logging.LevelDebug,
			zap.DebugLevel,
		},
		{
			logging.LevelInfo,
			zap.InfoLevel,
		},
		{
			logging.LevelWarn,
			zap.WarnLevel,
		},
		{
			logging.LevelError,
			zap.ErrorLevel,
		},
		{
			logging.Level(99),
			zap.InfoLevel,
		},
	} {
		ctx := grpc.NewContextWithServerTransportStream(t.Context(), contractTransport{method: "/jobs.Service/Read"})
		logger.Log(ctx, tc.level, "ignored", "text", "value", "count", 3, "ready", true, "details", []string{"safe"})
		entry := entries.All()[entries.Len()-1]
		require.Equal(t, tc.want, entry.Level)
		require.Equal(t, "Read", entry.Message)
		fields := entry.ContextMap()
		require.Equal(t, "value", fields["text"])
		require.EqualValues(t, 3, fields["count"])
		require.Equal(t, true, fields["ready"])
		require.Equal(t, []any{"safe"}, fields["details"])
	}
	logger.Log(grpc.NewContextWithServerTransportStream(t.Context(), contractTransport{method: "malformed"}), logging.LevelInfo, "")
	require.Equal(t, "unknown method", entries.All()[entries.Len()-1].Message)
	before := entries.Len()
	logger.Log(grpc.NewContextWithServerTransportStream(t.Context(), contractTransport{method: "/grpc.health.v1.Health/Check"}), logging.LevelInfo, "")
	require.Equal(t, before, entries.Len())
	for _, code := range []codes.Code{codes.OK, codes.InvalidArgument, codes.Internal, codes.Code(999)} {
		want := logging.LevelInfo
		if code == codes.InvalidArgument {
			want = logging.LevelWarn
		}
		if code == codes.Internal {
			want = logging.LevelError
		}
		require.Equal(t, want, serverCodeToLevel(code))
	}
}

func TestStreamLogFieldsUseValidatedIdentityAndTrace(t *testing.T) {
	t.Parallel()
	core, entries := observer.New(zapcore.InfoLevel)
	span := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}})
	ctx := trace.ContextWithSpanContext(t.Context(), span)
	ctx = auth.WithAudience(auth.WithRole(ctx, "admin"), "jobs-service")
	ctx = grpc.NewContextWithServerTransportStream(ctx, contractTransport{method: "/jobs.Service/Stream"})
	err := StreamLoggingInterceptor(zap.New(core))(nil,
		contractStream{ctx: ctx},
		&grpc.StreamServerInfo{
			FullMethod:     "/jobs.Service/Stream",
			IsServerStream: true,
		},
		func(any,
			grpc.ServerStream,
		) error {
			return status.Error(codes.Unavailable,
				"stream unavailable")
		})
	require.Equal(t, codes.Unavailable, status.Code(err))
	require.NotEmpty(t, entries.All())
	fields := entries.All()[entries.Len()-1].ContextMap()
	require.Equal(t, "jobs-service", fields["audience"])
	require.Equal(t, "admin", fields["role"])
	require.Equal(t, span.TraceID().String(), fields["trace_id"])
	require.Equal(t, span.SpanID().String(), fields["span_id"])
	require.Equal(t, "jobs.Service", fields["method"])
}

func TestRoleInterceptorsRejectMissingValidatedRole(t *testing.T) {
	t.Parallel()
	ctx := auth.WithAudience(t.Context(), "jobs-service")
	callback := func(string, string) bool { t.Fatal("missing role reached policy"); return false }
	_,
		err := UnaryRoleInterceptor(callback)(ctx,
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/jobs.Service/CancelJob"},
		func(context.Context,
			any) (any,
			error,
		) {
			t.Fatal("missing role reached handler")
			return struct{}{},
				nil
		})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	err = StreamRoleInterceptor(callback)(nil,
		contractStream{ctx: ctx},
		&grpc.StreamServerInfo{FullMethod: "/jobs.Service/StreamJobLogs"},
		func(any,
			grpc.ServerStream,
		) error {
			t.Fatal("missing role reached stream handler")
			return nil
		})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	called := false
	err = StreamRoleInterceptor(callback)(nil,
		contractStream{ctx: t.Context()},
		&grpc.StreamServerInfo{FullMethod: "/grpc.health.v1.Health/Watch"},
		func(any,
			grpc.ServerStream,
		) error {
			called = true
			return nil
		})
	require.NoError(t, err)
	require.True(t, called)
}
