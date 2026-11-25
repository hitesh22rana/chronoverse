package middlewares

import (
	"context"
	"slices"
	"strings"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	reflectionpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	reflectionalphapb "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
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

// AuthenticatedStreamInterceptor authenticates reflection and explicitly listed
// application methods. Application handlers own any request-specific authorization.
func AuthenticatedStreamInterceptor(
	logger *zap.Logger,
	tokens auth.IAuth,
	audience string,
	applicationMethods ...string,
) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if isHealthCheckRoute(info.FullMethod) {
			return handler(srv, stream)
		}

		authToken, err := auth.ExtractAuthorizationTokenFromMetadata(stream.Context())
		if err != nil {
			LogAuthenticationFailure(stream.Context(), logger, err)
			return err
		}

		ctx := auth.WithAuthorizationToken(stream.Context(), authToken)
		newCtx, _, err := tokens.ValidateToken(ctx, audience)
		if err != nil {
			LogAuthenticationFailure(ctx, logger, err)
			return err
		}

		switch info.FullMethod {
		case reflectionpb.ServerReflection_ServerReflectionInfo_FullMethodName,
			reflectionalphapb.ServerReflection_ServerReflectionInfo_FullMethodName:
			// Reflection exposes descriptors and has no owner-scoped request.
		default:
			if !slices.Contains(applicationMethods, info.FullMethod) {
				return status.Error(codes.PermissionDenied, "stream ownership policy is required")
			}
		}

		return handler(srv, &WrappedServerStream{
			ServerStream: stream,
			Ctx:          newCtx,
		})
	}
}

// StreamAudienceInterceptor propagates the JWT-validated audience from
// context. See UnaryAudienceInterceptor for the rationale — metadata is
// never trusted on authenticated RPCs.
func StreamAudienceInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if _, err := auth.ExtractAudienceFromContext(stream.Context()); err != nil {
			if !isHealthCheckRoute(info.FullMethod) {
				return err
			}
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
			if isHealthCheckRoute(info.FullMethod) {
				return handler(srv, stream)
			}
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
			logging.WithLevels(serverCodeToLevel),

			logging.WithLogOnEvents(
				logging.PayloadReceived,
				logging.FinishCall,
			),

			logging.WithFieldsFromContext(func(ctx context.Context) logging.Fields {
				fields := logging.Fields{}

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
