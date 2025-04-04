package client

import (
	"fmt"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/retry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// ServiceConfig contains the configuration for a gRPC service.
type ServiceConfig struct {
	Host     string
	Port     int
	Secure   bool
	CertFile string
}

// RetryConfig contains the configuration for retry behavior.
type RetryConfig struct {
	// MaxRetries is the maximum number of retries for a single request.
	MaxRetries uint
	// BackoffExponential is the base duration for exponential backoff.
	BackoffExponential time.Duration
	// RetryableCodes is a list of status codes that are retryable.
	RetryableCodes []codes.Code
}

// DefaultRetryConfig returns a default retry configuration.
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxRetries:         3,
		BackoffExponential: 100 * time.Millisecond,
		RetryableCodes: []codes.Code{
			codes.Unavailable,
			codes.ResourceExhausted,
			codes.DeadlineExceeded,
			codes.Aborted,
		},
	}
}

// NewClient creates a new gRPC client connection with retry and tracing support.
func NewClient(svcCfg ServiceConfig, retryCfg *RetryConfig) (*grpc.ClientConn, error) {
	// Set default retry config if not provided
	if retryCfg == nil {
		retryCfg = DefaultRetryConfig()
	}

	// Load credentials
	var creds credentials.TransportCredentials
	var err error
	if svcCfg.Secure {
		if creds, err = credentials.NewClientTLSFromFile(svcCfg.CertFile, ""); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to load TLS credentials: %v", err)
		}
	} else {
		creds = insecure.NewCredentials()
	}

	// Configure retry options
	retryOpts := []retry.CallOption{
		retry.WithMax(retryCfg.MaxRetries),
		retry.WithBackoff(
			retry.BackoffExponential(retryCfg.BackoffExponential),
		),
		retry.WithCodes(retryCfg.RetryableCodes...),
	}

	// Setup connection options
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		grpc.WithChainUnaryInterceptor(
			retry.UnaryClientInterceptor(retryOpts...),
		),
	}

	// Connect to the service
	conn, err := grpc.NewClient(
		fmt.Sprintf("%s:%d", svcCfg.Host, svcCfg.Port),
		dialOpts...,
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to connect to gRPC server: %v", err)
	}

	return conn, nil
}
