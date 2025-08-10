package middlewares

import (
	"context"
	"strings"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

// loggingInterceptor is a custom logging interceptor that uses zap logger.
//
//nolint:errcheck,forcetypeassert // It's safe to ignore all lint errors here.
func loggingInterceptor(l *zap.Logger) logging.Logger {
	return logging.LoggerFunc(func(ctx context.Context, lvl logging.Level, _ string, fields ...any) {
		// Skip logging for health check endpoints
		if method, ok := grpc.Method(ctx); ok && strings.HasPrefix(method, "/grpc.health.v1.Health/") {
			return
		}

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
			splits := strings.Split(method, "/")
			if len(splits) != 3 {
				msg = "unknown method"
			} else {
				msg = splits[2]
			}
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
		codes.OutOfRange:
		return logging.LevelWarn

	// Server errors - Error level
	case codes.Unknown,
		codes.DeadlineExceeded,
		codes.Canceled,
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
