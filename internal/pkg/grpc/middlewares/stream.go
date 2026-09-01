package middlewares

import (
	"context"
	"strings"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
)

// WrappedServerStream implements grpc.ServerStream and wraps the original stream to provide a custom context.
//
//nolint:containedctx // WrappedServerStream is a wrapper around grpc.ServerStream that allows to modify the context.
type WrappedServerStream struct {
	grpc.ServerStream
	Ctx context.Context
}

// Context returns the context of the wrapped stream.
func (w *WrappedServerStream) Context() context.Context {
	return w.Ctx
}

// StreamAudienceInterceptor propagates the JWT-validated audience from
// context. See UnaryAudienceInterceptor for the rationale — metadata is
// never trusted on authenticated RPCs.
func StreamAudienceInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if _, err := auth.ExtractAudienceFromContext(stream.Context()); err != nil {
			return err
		}

		return handler(srv, &WrappedServerStream{
			ServerStream: stream,
			Ctx:          stream.Context(),
		})
	}
}

// StreamRoleInterceptor reads the role from the JWT-validated context and
// dispatches the callback. The metadata "Role" header is intentionally
// NEVER consulted.
func StreamRoleInterceptor(callbackFunc RoleInterceptorCallbackFunc) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		role, err := auth.ExtractRoleFromContext(stream.Context())
		if err != nil {
			return err
		}

		if callbackFunc(info.FullMethod, role) {
			return status.Error(codes.PermissionDenied, "unauthorized access")
		}

		return handler(srv, &WrappedServerStream{
			ServerStream: stream,
			Ctx:          stream.Context(),
		})
	}
}

// StreamLoggingInterceptor returns a gRPC stream interceptor that logs the requests and responses.
func StreamLoggingInterceptor(logger *zap.Logger) grpc.StreamServerInterceptor {
	return logging.StreamServerInterceptor(
		loggingInterceptor(logger),
		[]logging.Option{
			// Log based on status code
			logging.WithLevels(serverCodeToLevel),

			// Only log when a call finishes
			logging.WithLogOnEvents(
				logging.PayloadReceived,
				logging.FinishCall,
			),

			// Add context information
			logging.WithFieldsFromContext(func(ctx context.Context) logging.Fields {
				fields := logging.Fields{}

				// Add trace and span IDs, this is useful for tracing and debugging
				// and can be used to correlate logs with traces.
				span := trace.SpanFromContext(ctx)
				if span.SpanContext().IsValid() {
					fields = append(fields,
						"trace_id", span.SpanContext().TraceID().String(),
						"span_id", span.SpanContext().SpanID().String(),
					)
				}

				if audience, err := auth.ExtractAudienceFromContext(ctx); err == nil {
					fields = append(fields, "audience", audience)
				}
				if role, err := auth.ExtractRoleFromContext(ctx); err == nil {
					fields = append(fields, "role", role)
				}
				if method, ok := grpc.Method(ctx); ok {
					fields = append(fields, "method", strings.Split(method, "/")[1])
				}

				return fields
			}),
		}...,
	)
}