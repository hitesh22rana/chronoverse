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
// It takes the method name and role as arguments and returns true if the role is valid.
// This is used to validate the role in the RoleInterceptor.
// This function is to be implemented by the service that uses the interceptor.
// If the role is not valid, the interceptor will return an error with code PermissionDenied.
type RoleInterceptorCallbackFunc func(method, role string) bool

// AudienceInterceptor sets the audience from the metadata and adds it to the context.
func AudienceInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Extract the audience from metadata.
		audience, err := auth.ExtractAudienceFromMetadata(ctx)
		if err != nil {
			return "", err
		}

		return handler(auth.WithAudience(ctx, audience), req)
	}
}

// RoleInterceptor extracts the role from the metadata and adds it to the context.
func RoleInterceptor(callbackFunc RoleInterceptorCallbackFunc) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Extract the role from metadata.
		role, err := auth.ExtractRoleFromMetadata(ctx)
		if err != nil {
			return "", err
		}

		// Validate the role using the callback function.
		if callbackFunc(info.FullMethod, role) {
			return "", status.Error(codes.PermissionDenied, "unauthorized access")
		}

		return handler(auth.WithRole(ctx, role), req)
	}
}

// LoggingInterceptor returns a gRPC interceptor that logs the requests and responses.
// It uses zap logger to log the messages.
func LoggingInterceptor(logger *zap.Logger) grpc.UnaryServerInterceptor {
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

				// Add the audience, role, auth token and method to the fields.
				// These fields are extracted from the context and added to the log.
				if audience, err := auth.ExtractAudienceFromMetadata(ctx); err == nil {
					fields = append(fields, "audience", audience)
				}
				if role, err := auth.ExtractRoleFromMetadata(ctx); err == nil {
					fields = append(fields, "role", role)
				}
				if authToken, err := auth.ExtractAuthorizationTokenFromMetadata(ctx); err == nil {
					fields = append(fields, "auth_token", authToken)
				}
				if method, ok := grpc.Method(ctx); ok {
					fields = append(fields, "method", strings.Split(method, "/")[1])
				}

				return fields
			}),
		}...,
	)
}

// loggingInterceptor is a custom logging interceptor that uses zap logger.
//
//nolint:errcheck,forcetypeassert // It's safe to ignore all lint errors here.
func loggingInterceptor(l *zap.Logger) logging.Logger {
	return logging.LoggerFunc(func(ctx context.Context, lvl logging.Level, _ string, fields ...any) {
		f := make([]zap.Field, 0, len(fields)/2)

		for i := 0; i < len(fields); i += 2 {
			key := fields[i]
			value := fields[i+1]

			switch v := value.(type) {
			case string:
				f = append(f, zap.String(key.(string), v))
			case int:
				f = append(f, zap.Int(key.(string), v))
			case bool:
				f = append(f, zap.Bool(key.(string), v))
			default:
				f = append(f, zap.Any(key.(string), v))
			}
		}

		logger := l.WithOptions(zap.AddCallerSkip(1)).With(f...)

		msg := ""
		if method, ok := grpc.Method(ctx); ok {
			msg = strings.Split(method, "/")[1]
		}
		switch lvl {
		case logging.LevelDebug:
			logger.Debug(msg)
		case logging.LevelInfo:
			logger.Info(msg)
		case logging.LevelWarn:
			logger.Warn(msg)
		case logging.LevelError:
			logger.Error(msg)
		default:
			logger.Info(msg)
		}
	})
}

// serverCodeToLevel maps gRPC status codes to logging levels.
func serverCodeToLevel(code codes.Code) logging.Level {
	switch code {
	// Success case
	case codes.OK:
		return logging.LevelInfo

	// Client errors - Warning level
	case codes.InvalidArgument,
		codes.NotFound,
		codes.AlreadyExists,
		codes.PermissionDenied,
		codes.Unauthenticated,
		codes.FailedPrecondition,
		codes.OutOfRange,
		codes.Canceled:
		return logging.LevelWarn

	// Server errors - Error level
	case codes.Unknown,
		codes.DeadlineExceeded,
		codes.ResourceExhausted,
		codes.Aborted,
		codes.Unimplemented,
		codes.Internal,
		codes.Unavailable,
		codes.DataLoss:
		return logging.LevelError

	// Default
	default:
		return logging.LevelInfo
	}
}
