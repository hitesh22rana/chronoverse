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
//nolint:containedctx // WrappedServerStream is a wrapper around grpc.ServerStream that allows us to modify the context.
type WrappedServerStream struct {
	grpc.ServerStream
	Ctx context.Context
}

// Context returns the context of the wrapped server stream.
func (w *WrappedServerStream) Context() context.Context {
	return w.Ctx
}

// StreamAudienceInterceptor extracts the audience from the metadata and adds it to the context.
// It returns a new wrapped stream with the modified context.
func StreamAudienceInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		// Extract the audience from metadata.
		audience, err := auth.ExtractAudienceFromMetadata(stream.Context())
		if err != nil {
			return err
		}

		return handler(srv, &WrappedServerStream{
			ServerStream: stream,
			Ctx:          auth.WithAudience(stream.Context(), audience),
		})
	}
}

// StreamRoleInterceptor extracts the role from the metadata and validates it using the provided callback function.
// If the role is not valid, it returns an error with code PermissionDenied.
func StreamRoleInterceptor(callbackFunc RoleInterceptorCallbackFunc) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		// Extract the role from metadata.
		role, err := auth.ExtractRoleFromMetadata(stream.Context())
		if err != nil {
			return err
		}

		// Validate the role using the callback function.
		if callbackFunc(info.FullMethod, role) {
			return status.Error(codes.PermissionDenied, "unauthorized access")
		}

		return handler(srv, &WrappedServerStream{
			ServerStream: stream,
			Ctx:          auth.WithRole(stream.Context(), role),
		})
	}
}

// StreamLoggingInterceptor returns a gRPC stream interceptor that logs the requests and responses.
// It uses zap logger to log the messages.
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

				if method, ok := grpc.Method(ctx); ok {
					fields = append(fields, "method", strings.Split(method, "/")[1])
				}

				return fields
			}),
		}...,
	)
}
