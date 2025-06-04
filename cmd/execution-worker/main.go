package main

import (
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	_ "go.uber.org/automaxprocs"
	"go.uber.org/zap"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	"github.com/hitesh22rana/chronoverse/internal/app/executor"
	"github.com/hitesh22rana/chronoverse/internal/config"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	grpcclient "github.com/hitesh22rana/chronoverse/internal/pkg/grpc/client"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/heartbeat"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
	executorrepo "github.com/hitesh22rana/chronoverse/internal/repository/executor"
	executorsvc "github.com/hitesh22rana/chronoverse/internal/service/executor"
)

const (
	// ExitOk and ExitError are the exit codes.
	ExitOk = iota
	// ExitError is the exit code for errors.
	ExitError
)

func main() {
	os.Exit(run())
}

func run() int {
	// Initialize the service with, all necessary components
	ctx, cancel := svcpkg.Init()
	defer cancel()

	// Handle OS signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
	}()

	// Load the execution service configuration
	cfg, err := config.InitExecutionJobConfig()
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

	// Initialize the kafka client
	kfk, err := kafka.New(ctx,
		kafka.WithBrokers(cfg.Kafka.Brokers...),
		kafka.WithProducerTopic(cfg.Kafka.ProducerTopic),
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

	// Connect to the workflows service
	workflowsConn, err := grpcclient.NewClient(
		grpcclient.ServiceConfig{
			Host:     cfg.WorkflowsService.Host,
			Port:     cfg.WorkflowsService.Port,
			Secure:   cfg.WorkflowsService.Secure,
			CertFile: cfg.WorkflowsService.CertFile,
		}, grpcclient.DefaultRetryConfig())
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return ExitError
	}
	defer workflowsConn.Close()

	// Connect to the jobs service
	jobsConn, err := grpcclient.NewClient(
		grpcclient.ServiceConfig{
			Host:     cfg.JobsService.Host,
			Port:     cfg.JobsService.Port,
			Secure:   cfg.JobsService.Secure,
			CertFile: cfg.JobsService.CertFile,
		}, grpcclient.DefaultRetryConfig())
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return ExitError
	}
	defer jobsConn.Close()

	// Connect to the notifications service
	notificationsConn, err := grpcclient.NewClient(
		grpcclient.ServiceConfig{
			Host:     cfg.NotificationsService.Host,
			Port:     cfg.NotificationsService.Port,
			Secure:   cfg.NotificationsService.Secure,
			CertFile: cfg.NotificationsService.CertFile,
		}, grpcclient.DefaultRetryConfig())
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return ExitError
	}
	defer notificationsConn.Close()

	// Initialize the execution job components
	repo := executorrepo.New(&executorrepo.Config{
		ParallelismLimit: cfg.ExecutionWorkerConfig.ParallelismLimit,
		ProducerTopic:    cfg.Kafka.ProducerTopic,
	}, auth, &executorrepo.Services{
		Workflows:     workflowspb.NewWorkflowsServiceClient(workflowsConn),
		Jobs:          jobspb.NewJobsServiceClient(jobsConn),
		Notifications: notificationspb.NewNotificationsServiceClient(notificationsConn),
		Csvc:          csvc,
		Hsvc:          heartbeat.New(),
	}, kfk)
	svc := executorsvc.New(repo)
	app := executor.New(ctx, svc)

	// Log the job information
	loggerpkg.FromContext(ctx).Info(
		"starting job",
		zap.Any("ctx", ctx),
		zap.String("name", svcpkg.Info().GetName()),
		zap.String("version", svcpkg.Info().GetVersion()),
		zap.String("environment", cfg.Environment.Env),
		zap.Int("gomaxprocs", runtime.GOMAXPROCS(0)),
	)

	// Run the execution job
	if err := app.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	return ExitOk
}
