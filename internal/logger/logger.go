// internal/logger/logger.go

package logger

import (
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	// Development is the development environment
	Development = "development"
	// Production is the production environment
	Production = "production"
)

// NewLogger initializes the logger
func NewLogger(environment string) (*zap.Logger, error) {
	var logger *zap.Logger
	var err error

	switch environment {
	case Production:
		logger, err = productionLogger()
	case Development:
		logger, err = developmentLogger()
	default:
		return nil, fmt.Errorf("unknown environment %s", environment)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to initialize logger: %v", err)
	}

	return logger, nil
}

// developmentLogger creates a development logger
func developmentLogger() (*zap.Logger, error) {
	config := zap.NewDevelopmentConfig()
	config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	return config.Build()
}

// productionLogger creates a production logger
func productionLogger() (*zap.Logger, error) {
	config := zap.NewProductionConfig()
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	return config.Build()
}
