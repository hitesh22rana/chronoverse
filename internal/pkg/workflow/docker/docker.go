package docker

import (
	"context"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Workflow represents a Docker workflow.
type Workflow struct {
	*client.Client
}

// New creates a new Workflow.
func New() (*Workflow, error) {
	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to initialize docker client: %v", err)
	}

	w := &Workflow{
		Client: cli,
	}

	if err := w.healthCheck(context.Background()); err != nil {
		return nil, err
	}

	return w, nil
}

func (w *Workflow) healthCheck(ctx context.Context) error {
	// Health check the Docker client
	if _, err := w.Client.Ping(ctx); err != nil {
		return status.Errorf(codes.Internal, "failed to ping docker client: %v", err)
	}

	return nil
}

// Execute runs a command in a new container and streams the logs.
//
//nolint:gocyclo, gocritic // This function is not complex enough to warrant a refactor
func (w *Workflow) Execute(
	ctx context.Context,
	timeout time.Duration,
	image string,
	cmd []string,
) (<-chan string, <-chan error, error) {
	if err := w.healthCheck(ctx); err != nil {
		return nil, nil, err
	}

	containerTimeout := int(timeout.Seconds())

	// Create container with auto-removal
	resp, err := w.Client.ContainerCreate(
		ctx,
		&container.Config{
			Image:       image,
			Cmd:         cmd,
			StopTimeout: &containerTimeout,
		},
		&container.HostConfig{
			AutoRemove: true,
		},
		nil, nil, "",
	)
	if err != nil {
		return nil, nil, status.Errorf(codes.FailedPrecondition, "failed to create container: %v", err)
	}

	containerID := resp.ID
	startTime := time.Now()

	// Start the container
	if err := w.Client.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return nil, nil, status.Errorf(codes.FailedPrecondition, "failed to start container: %v", err)
	}

	// Create channels for logs and errors
	logs := make(chan string)
	errs := make(chan error)

	// Stream logs and handle container completion
	go func() {
		defer close(logs)
		defer close(errs)

		// Stream logs
		streamedLogs, err := w.Client.ContainerLogs(ctx, containerID, container.LogsOptions{
			ShowStdout: true,
			ShowStderr: true,
			Follow:     true,
		})
		if err != nil {
			// To distinguish between Docker daemon unavailability and other errors
			if client.IsErrConnectionFailed(err) {
				errs <- status.Errorf(codes.Unavailable, "docker daemon unavailable: %v", err)
			} else {
				errs <- status.Errorf(codes.FailedPrecondition, "failed to get container logs: %v", err)
			}
			return
		}
		defer streamedLogs.Close()

		// Read logs in a separate goroutine
		logsDone := make(chan struct{})
		go func() {
			defer close(logsDone)
			buf := make([]byte, 4096)
			for {
				n, err := streamedLogs.Read(buf)
				if n > 0 {
					logs <- string(buf[:n])
				}
				if err != nil {
					return
				}
			}
		}()

		// Wait for container completion to get exit code
		statusCh, waitErrCh := w.Client.ContainerWait(ctx, containerID, container.WaitConditionRemoved)
		select {
		case err := <-waitErrCh:
			if err != nil {
				// Return early if the container was already removed
				if strings.Contains(err.Error(), "No such container") {
					return
				}

				// Check if this is a context timeout/cancel
				if ctx.Err() != nil && (ctx.Err() == context.DeadlineExceeded || ctx.Err() == context.Canceled) {
					errs <- status.Errorf(codes.DeadlineExceeded, "container execution timed out: %v", ctx.Err())
				} else {
					errs <- status.Errorf(codes.Aborted, "container execution error: %v", err)
				}
			}
		case containerStatus := <-statusCh:
			if containerStatus.StatusCode != 0 {
				errs <- status.Errorf(codes.Aborted, "container exited with non-zero code: %d", containerStatus.StatusCode)
			}
			executionTime := time.Since(startTime)
			if executionTime > timeout {
				errs <- status.Errorf(codes.DeadlineExceeded, "container execution timed out: %v", executionTime)
			}
		case <-ctx.Done():
			errs <- status.Errorf(codes.DeadlineExceeded, "container execution timed out: %v", ctx.Err())
		}

		// Wait for logs to finish streaming or timeout
		select {
		case <-logsDone:
			// All logs have been read
		case <-time.After(time.Second):
			// If logs are taking too long, continue anyway
		}
	}()

	return logs, errs, nil
}
