package main

import (
	"context"
	"fmt"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	userpb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	"github.com/hitesh22rana/chronoverse/internal/config"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	"github.com/hitesh22rana/chronoverse/internal/pkg/crypto"
	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
	"github.com/hitesh22rana/chronoverse/internal/server"
)

const (
	// ExitOk and ExitError are the exit codes.
	ExitOk = iota
	// ExitError is the exit code for errors.
	ExitError
)

var (
	// version is the service version.
	version string

	// name is the name of the service.
	name string

	// authPrivateKeyPath is the path to the private key.
	authPrivateKeyPath string

	// authPublicKeyPath is the path to the public key.
	authPublicKeyPath string
)

func main() {
	os.Exit(run())
}

func run() int {
	// Global context to cancel the execution
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize the service information
	initSvcInfo()

	// Load the server configuration
	cfg, err := config.InitServerConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	// Initialize the auth issuer
	auth, err := auth.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	// Initialize the crypto module
	crypto, err := crypto.New(cfg.Crypto.Secret)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	// Load the users service credentials
	var creds credentials.TransportCredentials
	if cfg.UsersService.Secure {
		creds, err = loadTLSCredentials(cfg.UsersService.CertFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load TLS credentials: %v\n", err)
			return ExitError
		}
	} else {
		creds = insecure.NewCredentials()
	}

	// Connect to the users service
	usersConn, err := grpc.NewClient(
		fmt.Sprintf("%s:%d", cfg.UsersService.Host, cfg.UsersService.Port),
		grpc.WithTransportCredentials(creds),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to auth gRPC server: %v\n", err)
		return ExitError
	}
	defer usersConn.Close()

	// Load the workflows service credentials
	if cfg.WorkflowsService.Secure {
		creds, err = loadTLSCredentials(cfg.WorkflowsService.CertFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load TLS credentials: %v\n", err)
			return ExitError
		}
	} else {
		creds = insecure.NewCredentials()
	}

	// Connect to the workflows service
	workflowsConn, err := grpc.NewClient(
		fmt.Sprintf("%s:%d", cfg.WorkflowsService.Host, cfg.WorkflowsService.Port),
		grpc.WithTransportCredentials(creds),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to workflows gRPC server: %v\n", err)
		return ExitError
	}
	defer workflowsConn.Close()

	// Load the jobs service credentials
	if cfg.JobsService.Secure {
		creds, err = loadTLSCredentials(cfg.JobsService.CertFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load TLS credentials: %v\n", err)
			return ExitError
		}
	} else {
		creds = insecure.NewCredentials()
	}

	// Connect to the jobs service
	jobsConn, err := grpc.NewClient(
		fmt.Sprintf("%s:%d", cfg.JobsService.Host, cfg.JobsService.Port),
		grpc.WithTransportCredentials(creds),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to jobs gRPC server: %v\n", err)
		return ExitError
	}
	defer jobsConn.Close()

	// Initialize the redis store
	rdb, err := redis.New(ctx, &redis.Config{
		Host:                     cfg.Redis.Host,
		Port:                     cfg.Redis.Port,
		Password:                 cfg.Redis.Password,
		DB:                       cfg.Redis.DB,
		PoolSize:                 cfg.Redis.PoolSize,
		MinIdleConns:             cfg.Redis.MinIdleConns,
		ReadTimeout:              cfg.Redis.ReadTimeout,
		WriteTimeout:             cfg.Redis.WriteTimeout,
		MaxMemory:                cfg.Redis.MaxMemory,
		EvictionPolicy:           cfg.Redis.EvictionPolicy,
		EvictionPolicySampleSize: cfg.Redis.EvictionPolicySampleSize,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer rdb.Close()

	usersServiceClient := userpb.NewUsersServiceClient(usersConn)
	workflowsServiceClient := workflowspb.NewWorkflowsServiceClient(workflowsConn)
	jobsServiceClient := jobspb.NewJobsServiceClient(jobsConn)
	srv := server.New(&server.Config{
		Host:              cfg.Server.Host,
		Port:              cfg.Server.Port,
		RequestTimeout:    cfg.Server.RequestTimeout,
		ReadTimeout:       cfg.Server.ReadTimeout,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
		ValidationConfig: &server.ValidationConfig{
			SessionExpiry:    cfg.Server.SessionExpiry,
			CSRFExpiry:       cfg.Server.CSRFExpiry,
			RequestBodyLimit: cfg.Server.RequestBodyLimit,
			CSRFHMACSecret:   cfg.Server.CSRFHMACSecret,
		},
	}, auth, crypto, rdb, usersServiceClient, workflowsServiceClient, jobsServiceClient)

	fmt.Fprintln(os.Stdout, "Starting HTTP server on port 8080",
		fmt.Sprintf("name: %s, version: %s",
			svcpkg.Info().GetName(),
			svcpkg.Info().GetVersion(),
		),
	)

	if err := srv.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	return ExitOk
}

// initSvcInfo initializes the service information.
func initSvcInfo() {
	svcpkg.SetVersion(version)
	svcpkg.SetName(name)
	svcpkg.SetAuthPrivateKeyPath(authPrivateKeyPath)
	svcpkg.SetAuthPublicKeyPath(authPublicKeyPath)
}

// loadTLSCredentials loads the TLS credentials from the certificate file.
func loadTLSCredentials(certFile string) (credentials.TransportCredentials, error) {
	creds, err := credentials.NewClientTLSFromFile(certFile, "")
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS credentials: %w", err)
	}

	return creds, nil
}
