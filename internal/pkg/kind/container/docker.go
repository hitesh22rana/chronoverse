package container

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
)

const (
	// containerStopTimeout is the default timeout for stopping a container.
	containerStopTimeout = 2 * time.Second
)

// DockerWorkflow represents a Docker workflow.
type DockerWorkflow struct {
	*client.Client
}

// NewDockerWorkflow creates a new DockerWorkflow.
func NewDockerWorkflow() (*DockerWorkflow, error) {
	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to initialize docker client: %v", err)
	}

	w := &DockerWorkflow{
		Client: cli,
	}

	if err := w.healthCheck(context.Background()); err != nil {
		return nil, err
	}

	return w, nil
}

func (w *DockerWorkflow) healthCheck(ctx context.Context) error {
	// Health check the Docker client
	if _, err := w.Client.Ping(ctx); err != nil {
		return status.Errorf(codes.Internal, "failed to ping docker client: %v", err)
	}

	return nil
}

// Execute runs a command in a new container and streams the logs.
//
//nolint:gocyclo,gocritic // This function is not complex enough to warrant a refactor
func (w *DockerWorkflow) Execute(
	ctx context.Context,
	timeout time.Duration,
	image string,
	cmd []string,
	env []string,
) (string, <-chan *jobsmodel.JobLog, <-chan error, error) {
	if err := w.healthCheck(ctx); err != nil {
		return "", nil, nil, err
	}

	containerTimeout := int(timeout.Seconds())

	// Create container with auto-removal
	resp, err := w.Client.ContainerCreate(
		ctx,
		&container.Config{
			Image:       image,
			Cmd:         cmd,
			StopTimeout: &containerTimeout,
			Env:         env,
		},
		&container.HostConfig{
			AutoRemove: true,
		},
		nil, nil, "",
	)
	if err != nil {
		return "", nil, nil, status.Errorf(codes.FailedPrecondition, "failed to create container: %v", err)
	}

	containerID := resp.ID

	// Start the container
	if err := w.Client.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return "", nil, nil, status.Errorf(codes.FailedPrecondition, "failed to start container: %v", err)
	}

	// Channel for logs streaming
	logs := make(chan *jobsmodel.JobLog)
	// Channel to capture errors
	errs := make(chan error)

	// Create a context with timeout for this container
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)

	// Stream logs and handle container completion
	go func() {
		defer close(logs)
		defer close(errs)
		defer cancel()

		// Set up container wait early to detect completion
		statusCh, waitErrCh := w.Client.ContainerWait(timeoutCtx, containerID, container.WaitConditionRemoved)

		// Start log streaming
		logsDone := make(chan struct{})
		go func() {
			defer close(logsDone)
			w.streamContainerLogs(timeoutCtx, containerID, logs, errs)
		}()

		// Monitor for timeouts and container completion
		select {
		case <-timeoutCtx.Done():
			// Container execution timed out
			errs <- status.Errorf(codes.DeadlineExceeded, "container execution timed out: %v", timeoutCtx.Err())

			// Container execution timed out - try to stop the container
			stopTimeout := int(containerStopTimeout.Seconds())
			//nolint:errcheck,contextcheck // Ignore error, as we are trying to stop the container gracefully
			_ = w.Client.ContainerStop(context.Background(), containerID, container.StopOptions{
				Timeout: &stopTimeout,
			})

			// Wait for logs to finish
			select {
			case <-logsDone:
			case <-time.After(100 * time.Millisecond):
			}
			return

		case err := <-waitErrCh:
			// Return early if the container was already removed
			if strings.Contains(err.Error(), "No such container") {
				// Wait for any remaining logs
				select {
				case <-logsDone:
				case <-time.After(100 * time.Millisecond):
				}
				return
			}

			// Check if this is a context timeout/cancel
			if ctx.Err() != nil && (ctx.Err() == context.DeadlineExceeded || ctx.Err() == context.Canceled) {
				errs <- status.Errorf(codes.DeadlineExceeded, "container execution timed out: %v", ctx.Err())
			} else {
				errs <- status.Errorf(codes.Aborted, "container execution error: %v", err)
			}

		case containerStatus := <-statusCh:
			// Check exit code after logs finish
			//nolint:gosimple // Ignore error, as this is a best-effort operation
			_ = <-logsDone

			if containerStatus.StatusCode != 0 {
				errs <- status.Errorf(codes.Aborted, "container exited with non-zero code: %d", containerStatus.StatusCode)
			}
		}
	}()

	return containerID, logs, errs, nil
}

// streamContainerLogs streams container logs and properly demuxes stdout/stderr.
//
//nolint:gocyclo // This function is not complex enough to warrant a refactor
func (w *DockerWorkflow) streamContainerLogs(ctx context.Context, containerID string, logCh chan<- *jobsmodel.JobLog, errs chan<- error) {
	reader, err := w.Client.ContainerLogs(ctx, containerID, container.LogsOptions{
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
	defer reader.Close()

	var sequenceNum uint32

	// Use pipes to receive stdout and stderr separately
	stdoutReader, stdoutWriter := io.Pipe()
	stderrReader, stderrWriter := io.Pipe()

	// Channel to collect log messages from both streams
	logMessages := make(chan *jobsmodel.JobLog)

	// Start demuxing in a goroutine
	go func() {
		defer stdoutWriter.Close()
		defer stderrWriter.Close()
		_, err := stdcopy.StdCopy(stdoutWriter, stderrWriter, reader)
		if err != nil && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe)) {
			// Container might have been removed or an error occurred
			if client.IsErrConnectionFailed(err) {
				errs <- status.Errorf(codes.Unavailable, "docker daemon unavailable: %v", err)
			}
		}
	}()

	// Wait group to track when both readers are done
	var wg sync.WaitGroup

	// Read from stdout
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer stdoutReader.Close()

		scanner := bufio.NewScanner(stdoutReader)
		for scanner.Scan() {
			msg := scanner.Text()
			if msg != "" {
				select {
				case logMessages <- &jobsmodel.JobLog{Timestamp: time.Now(), Message: msg, SequenceNum: atomic.LoadUint32(&sequenceNum), Stream: "stdout"}:
					// Atomic increment the sequence number for each log entry
					atomic.AddUint32(&sequenceNum, 1)
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	// Read from stderr
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer stderrReader.Close()

		scanner := bufio.NewScanner(stderrReader)
		for scanner.Scan() {
			msg := scanner.Text()
			if msg != "" {
				select {
				case logMessages <- &jobsmodel.JobLog{Timestamp: time.Now(), Message: msg, SequenceNum: atomic.LoadUint32(&sequenceNum), Stream: "stderr"}:
					// Atomic increment the sequence number for each log entry
					atomic.AddUint32(&sequenceNum, 1)
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	// Close logMessages when both readers are done
	go func() {
		wg.Wait()
		close(logMessages)
	}()

	// Forward log messages to the output channel
	for {
		select {
		case msg, ok := <-logMessages:
			if !ok {
				// Channel closed, all logs processed
				return
			}

			select {
			case logCh <- msg:
			case <-ctx.Done():
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// Build pulls an image from the registry, required for the image to be available locally.
func (w *DockerWorkflow) Build(ctx context.Context, imageName string) error {
	if err := w.healthCheck(ctx); err != nil {
		return err
	}

	out, err := w.Client.ImagePull(ctx, imageName, image.PullOptions{})
	if err != nil {
		return status.Errorf(codes.NotFound, "failed to pull image: %v", err)
	}
	defer out.Close()

	// Read the output to completion, required to properly register the image in Docker desktop
	_, err = io.Copy(io.Discard, out)
	if err != nil {
		return status.Errorf(codes.FailedPrecondition, "failed to read image pull output: %v", err)
	}

	return nil
}

// Terminate stops a running container by its unique containerID.
func (w *DockerWorkflow) Terminate(ctx context.Context, containerID string) error {
	if err := w.healthCheck(ctx); err != nil {
		return err
	}

	stopTimeout := int(containerStopTimeout.Seconds())
	if err := w.Client.ContainerStop(ctx, containerID, container.StopOptions{
		Timeout: &stopTimeout,
	}); err != nil {
		return status.Errorf(codes.Aborted, "failed to stop container %s: %v", containerID, err)
	}

	return nil
}
