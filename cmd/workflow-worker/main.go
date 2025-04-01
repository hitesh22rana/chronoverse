package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	jobpb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	"github.com/hitesh22rana/chronoverse/internal/app/workflow"
	"github.com/hitesh22rana/chronoverse/internal/config"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	otelpkg "github.com/hitesh22rana/chronoverse/internal/pkg/otel"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
	workflowrepo "github.com/hitesh22rana/chronoverse/internal/repository/workflow"
	workflowsvc "github.com/hitesh22rana/chronoverse/internal/service/workflow"
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

//nolint:gocyclo // This function is necessary to run the service.
func run() int {
	// Global context to cancel the execution
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize the service information
	initSvcInfo()

	// Handle OS signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
	}()

	// Load the workflow service configuration
	cfg, err := config.InitWorkflowWorkerConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	// Initialize the OpenTelemetry Resource
	res, err := otelpkg.InitResource(ctx, svcpkg.Info().GetName(), svcpkg.Info().GetVersion())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	// Initialize the OpenTelemetry TracerProvider
	tp, err := otelpkg.InitTracerProvider(ctx, res)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer func() {
		if err = tp.Shutdown(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "failed to shutdown tracer provider: %v\n", err)
		}
	}()

	// Initialize the OpenTelemetry MeterProvider
	mp, err := otelpkg.InitMeterProvider(ctx, res)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer func() {
		if err = mp.Shutdown(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "failed to shutdown meter provider: %v\n", err)
		}
	}()

	// Initialize the OpenTelemetry LoggerProvider
	lp, err := otelpkg.InitLogProvider(ctx, res)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer func() {
		if err = lp.Shutdown(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "failed to shutdown logger provider: %v\n", err)
		}
	}()

	// Initialize and set the logger in the context
	ctx, logger := loggerpkg.Init(ctx, svcpkg.Info().GetName(), lp)
	defer func() {
		if err = logger.Sync(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to sync logger: %v\n", err)
		}
	}()

	// Initialize the auth issuer
	auth, err := auth.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	// Initialize the kafka client
	kfk, err := kafka.New(ctx,
		kafka.WithBrokers(cfg.Kafka.Brokers...),
		kafka.WithConsumerGroup(cfg.Kafka.ConsumerGroup),
		kafka.WithConsumeTopics(cfg.Kafka.ConsumeTopics...),
		kafka.WithDisableAutoCommit(),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer kfk.Close()

	// Initialize the container service
	csvc, err := container.NewDockerWorkflow()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	// Load the workflows service credentials
	var creds credentials.TransportCredentials
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

	// Load the notifications service credentials
	if cfg.NotificationsService.Secure {
		creds, err = loadTLSCredentials(cfg.NotificationsService.CertFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load TLS credentials: %v\n", err)
			return ExitError
		}
	} else {
		creds = insecure.NewCredentials()
	}

	// Connect to the notifications service
	notificationsConn, err := grpc.NewClient(
		fmt.Sprintf("%s:%d", cfg.NotificationsService.Host, cfg.NotificationsService.Port),
		grpc.WithTransportCredentials(creds),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to notifications gRPC server: %v\n", err)
		return ExitError
	}
	defer notificationsConn.Close()

	// Initialize the workflow job components
	repo := workflowrepo.New(&workflowrepo.Config{
		ParallelismLimit: cfg.WorkflowWorkerConfig.ParallelismLimit,
	}, auth, &workflowrepo.Services{
		Workflows:     workflowspb.NewWorkflowsServiceClient(workflowsConn),
		Jobs:          jobpb.NewJobsServiceClient(jobsConn),
		Notifications: notificationspb.NewNotificationsServiceClient(notificationsConn),
		Csvc:          csvc,
	}, kfk)
	svc := workflowsvc.New(repo)
	app := workflow.New(ctx, svc)

	// Log the job information
	logger.Info(
		"starting job",
		zap.Any("ctx", ctx),
		zap.String("name", svcpkg.Info().Name),
		zap.String("version", svcpkg.Info().Version),
		zap.String("environment", cfg.Environment.Env),
	)

	// Run the workflow job
	if err := app.Run(ctx); err != nil {
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
