package logger

import (
	"context"
	"os"

	"go.opentelemetry.io/contrib/bridges/otelzap"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// loggerKey is the key for the logger in the context.
type loggerKey struct{}

// Init initializes a new logger with the OTLP gRPC exporter and sets it in the context.
func Init(ctx context.Context, serviceInfo string, lp *sdklog.LoggerProvider) (context.Context, *zap.Logger) {
	logger := zap.New(
		zapcore.NewTee(
			zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(os.Stdout), zapcore.InfoLevel),
			otelzap.NewCore(serviceInfo, otelzap.WithLoggerProvider(lp)),
		),
	)

	return context.WithValue(ctx, loggerKey{}, logger), logger
}

// FromContext extracts the logger from the context.
func FromContext(ctx context.Context) *zap.Logger {
	value := ctx.Value(loggerKey{})
	if value == nil {
		return zap.NewNop()
	}

	logger, ok := value.(*zap.Logger)
	if !ok {
		return zap.NewNop()
	}

	return logger
}
