package main

import (
	"fmt"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/hitesh22rana/chronoverse/internal/config"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
	"github.com/hitesh22rana/chronoverse/internal/server"
	pb "github.com/hitesh22rana/chronoverse/pkg/proto/go"
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
)

func main() {
	os.Exit(run())
}

func run() int {
	// Initialize the service information
	initSvcInfo()

	// Load the server configuration
	cfg, err := config.InitServerConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	authConn, err := grpc.NewClient(
		fmt.Sprintf("%s:%d", cfg.Auth.Host, cfg.Auth.Port),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to auth gRPC server: %v\n", err)
		return ExitError
	}
	defer authConn.Close()

	client := pb.NewAuthServiceClient(authConn)
	srv := server.New(&server.Config{
		Host:              cfg.Server.Host,
		Port:              cfg.Server.Port,
		RequestTimeout:    cfg.Server.RequestTimeout,
		ReadTimeout:       cfg.Server.ReadTimeout,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
		RequestBodyLimit:  cfg.Server.RequestBodyLimit,
	}, client)

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
}
