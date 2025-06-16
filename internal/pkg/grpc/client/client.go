package client

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/retry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/status"
)

// TLSConfig holds the TLS configuration for gRPC client.
type TLSConfig struct {
	Enabled        bool
	CAFile         string
	ClientCertFile string
	ClientKeyFile  string
}

// ServiceConfig contains the configuration for a gRPC service.
type ServiceConfig struct {
	Host string
	Port int
	TLS  *TLSConfig
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
func NewClient(svcCfg *ServiceConfig, retryCfg *RetryConfig) (*grpc.ClientConn, error) {
	var opts []grpc.DialOption

	// Configure TLS or insecure connection
	if svcCfg.TLS.Enabled {
		creds, err := loadTLSCredentials(svcCfg.TLS)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to load TLS credentials: %v", err)
		}
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

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

// loadTLSCredentials loads TLS credentials for the client.
func loadTLSCredentials(tlsCfg *TLSConfig) (credentials.TransportCredentials, error) {
	// Load CA certificate
	caCert, err := os.ReadFile(tlsCfg.CAFile)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to read CA certificate: %v", err)
	}

	caCertPool := x509.NewCertPool()
	if ok := caCertPool.AppendCertsFromPEM(caCert); !ok {
		return nil, status.Errorf(codes.Internal, "failed to append CA certificate to pool")
	}

	// Load client certificate and private key
	clientCert, err := tls.LoadX509KeyPair(tlsCfg.ClientCertFile, tlsCfg.ClientKeyFile)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to load client certificate and key: %v", err)
	}

	config := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      caCertPool,
		MinVersion:   tls.VersionTLS12,
	}

	return credentials.NewTLS(config), nil
}
