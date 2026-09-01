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

// RoleInterceptorCallbackFunc is a callback function that checks if the role is valid for the method.
// It takes the method name and role as arguments and returns true if the role is invalid.
// This is used to validate the role in the RoleInterceptor.
// This function is to be implemented by the service that uses the interceptor.
// If the role is not valid, the interceptor will return an error with code PermissionDenied.
type RoleInterceptorCallbackFunc func(method, role string) bool

// UnaryAudienceInterceptor propagates the JWT-validated audience from context
// for log enrichment. It does NOT trust the metadata header on authenticated
// RPCs — that audience is established by ValidateToken and must already be
// present in context when this interceptor runs.
//
// Health-check RPCs and other exempt routes are intentionally allowed to
// pass through without a validated audience; the authToken interceptor
// skips them and the logging fields will simply omit "audience" / "role"
// for those calls.
func UnaryAudienceInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if _, err := auth.ExtractAudienceFromContext(ctx); err != nil {
			if !isHealthCheckRoute(info.FullMethod) {
				return nil, err
			}
		}

		return handler(ctx, req)
	}
}

// UnaryRoleInterceptor reads the role from the JWT-validated context (set
// by ValidateToken) and dispatches the callback. The metadata "Role" header
// is intentionally NEVER consulted — see the C2 finding in
// SECURITY_ASSESSMENT_STACK_C.md.
func UnaryRoleInterceptor(callbackFunc RoleInterceptorCallbackFunc) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		role, err := auth.ExtractRoleFromContext(ctx)
		if err != nil {
			if isHealthCheckRoute(info.FullMethod) {
				return handler(ctx, req)
			}
			return nil, err
		}

		if callbackFunc(info.FullMethod, role) {
			return nil, status.Error(codes.PermissionDenied, "unauthorized access")
		}

		return handler(ctx, req)
	}
}

// isHealthCheckRoute returns true when the gRPC method is a health check.
// Health checks bypass ValidateToken so they have no audience/role in
// context; the audience/role interceptors must tolerate that.
func isHealthCheckRoute(method string) bool {
	return strings.Contains(method, "/grpc.health.v1.Health/")
}

// UnaryLoggingInterceptor returns a gRPC unary interceptor that logs the requests and responses.
// It uses zap logger to log the messages.
func UnaryLoggingInterceptor(logger *zap.Logger) grpc.UnaryServerInterceptor {
	return logging.UnaryServerInterceptor(
		loggingInterceptor(logger),
		[]logging.Option{
			// Log based on status code
			logging.WithLevels(serverCodeToLevel),

			// Only log when a call finishes
			logging.WithLogOnEvents(
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

				// Audience and role are now read from the JWT-validated
				// context (set by ValidateToken). The metadata headers
				// are intentionally NOT consulted.
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
