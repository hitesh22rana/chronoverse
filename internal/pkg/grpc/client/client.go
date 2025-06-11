package client

import (
	"fmt"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/retry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding/gzip"
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
	// PerRetryTimeout is the timeout for each retry attempt (handles case for context.DeadlineExceeded and context.Canceled).
	PerRetryTimeout time.Duration
}

// DefaultRetryConfig returns a default retry configuration.
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxRetries:         3,
		BackoffExponential: 100 * time.Millisecond,
		RetryableCodes: []codes.Code{
			codes.Unavailable,
			codes.ResourceExhausted,
			codes.Aborted,
		},
		PerRetryTimeout: 2 * time.Second,
	}
}

// NewClient creates a new gRPC client connection with retry and tracing support.
func NewClient(svcCfg ServiceConfig, retryCfg *RetryConfig) (*grpc.ClientConn, error) {
	var opts []grpc.DialOption

	// Set default retry config if not provided
	if retryCfg == nil {
		retryCfg = DefaultRetryConfig()
	}

	// Configure retry options
	retryOpts := []retry.CallOption{
		retry.WithCodes(retryCfg.RetryableCodes...),
		retry.WithMax(retryCfg.MaxRetries),
		retry.WithBackoff(retry.BackoffExponential(retryCfg.BackoffExponential)),
		retry.WithPerRetryTimeout(retryCfg.PerRetryTimeout),
	}

	opts = append(
		opts,
		grpc.WithUnaryInterceptor(retry.UnaryClientInterceptor(retryOpts...)),
		grpc.WithStreamInterceptor(retry.StreamClientInterceptor(retryOpts...)),
		grpc.WithDefaultCallOptions(grpc.UseCompressor(gzip.Name)),
	)

	// Load credentials
	if svcCfg.Secure {
		creds, err := credentials.NewClientTLSFromFile(svcCfg.CertFile, "")
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to load TLS credentials: %v", err)
		}
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// Connect to the service
	conn, err := grpc.NewClient(
		fmt.Sprintf("%s:%d", svcCfg.Host, svcCfg.Port),
		opts...,
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to connect to gRPC server: %v", err)
	}

	return conn, nil
}
