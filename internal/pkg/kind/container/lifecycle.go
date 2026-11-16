package container

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/terminalreason"
)

// Creation outcomes distinguish rejected input from uncertain mutations.
const (
	CreationOutcomeRejected = "REJECTED"
	CreationOutcomeSystem   = "SYSTEM"
	CreationOutcomeUnknown  = "UNKNOWN"
	CreationOutcomeConflict = "CONFLICT"
)

// Ownership label keys are part of the Docker materialization protocol.
const (
	IntentIDLabel = "chronoverse.intent_id"
	JobIDLabel    = "chronoverse.job_id"
	AttemptLabel  = "chronoverse.attempt"
)

// CreateRequest is a frozen, immutable container creation snapshot passed by value across the adapter boundary.
type CreateRequest struct {
	Name, IntentID, JobID string
	Attempt               int32
	Image                 string
	Cmd, Env              []string
	Timeout               time.Duration
}

type creationError struct {
	outcome string
	err     error
}

func (e *creationError) Error() string              { return e.err.Error() }
func (e *creationError) Unwrap() error              { return e.err }
func (e *creationError) GRPCStatus() *status.Status { return status.Convert(e.err) }

// CreationOutcome returns the typed creation classification carried by err.
func CreationOutcome(err error) string {
	var classified *creationError
	if errors.As(err, &classified) {
		return classified.outcome
	}
	if reason, ok := terminalreason.FromError(err); ok && reason != terminalreason.SystemError {
		return CreationOutcomeRejected
	}
	return CreationOutcomeSystem
}

func lifecycleError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return &creationError{
			outcome: CreationOutcomeUnknown,
			err:     terminalreason.Wrap(terminalreason.SystemError, status.FromContextError(ctx.Err()).Err()),
		}
	}
	if cerrdefs.IsConflict(err) {
		return &creationError{
			outcome: CreationOutcomeConflict,
			err:     terminalreason.Wrap(terminalreason.SystemError, status.Errorf(codes.AlreadyExists, "container name conflict: %v", err)),
		}
	}
	if cerrdefs.IsInvalidArgument(err) {
		return &creationError{
			outcome: CreationOutcomeRejected,
			err:     status.Errorf(codes.InvalidArgument, "invalid container configuration: %v", err),
		}
	}
	outcome := CreationOutcomeUnknown
	if cerrdefs.IsNotFound(err) || cerrdefs.IsUnauthorized(err) || cerrdefs.IsPermissionDenied(err) {
		outcome = CreationOutcomeSystem
	}
	if client.IsErrConnectionFailed(err) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		outcome = CreationOutcomeUnknown
	}
	return &creationError{
		outcome: outcome,
		err:     terminalreason.Wrap(terminalreason.SystemError, status.Errorf(codes.Unavailable, "container mutation failed: %v", err)),
	}
}

// RuntimeInstance returns the immutable identity of the connected Docker daemon.
func (w *DockerWorkflow) RuntimeInstance(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	data, err := w.Client.Info(ctx, client.InfoOptions{})
	if err != nil {
		return "", lifecycleError(ctx, err)
	}
	if data.Info.ID == "" {
		return "", status.Error(codes.Unavailable, "docker instance identity is missing")
	}
	return data.Info.ID, nil
}

// Create is one named mutation; a failed reply never authorizes a second transmission.
func (w *DockerWorkflow) Create(ctx context.Context, req CreateRequest) (string, error) { //nolint:gocritic // hugeParam: CreateRequest is an immutable snapshot frozen at this boundary.
	if req.Name == "" || req.IntentID == "" || req.JobID == "" || req.Attempt <= 0 || req.Image == "" || req.Timeout <= 0 {
		return "", &creationError{
			outcome: CreationOutcomeRejected,
			err:     status.Error(codes.InvalidArgument, "container identity and workload are required"),
		}
	}
	if err := w.healthCheck(ctx); err != nil {
		return "", &creationError{outcome: CreationOutcomeSystem, err: terminalreason.Wrap(terminalreason.SystemError, err)}
	}
	if err := w.ensureWorkloadNetwork(ctx); err != nil {
		return "", &creationError{
			outcome: CreationOutcomeSystem,
			err:     terminalreason.Wrap(terminalreason.SystemError, status.Errorf(codes.Unavailable, "workload network is not ready: %v", err)),
		}
	}
	timeout := int(req.Timeout.Seconds())
	config := &container.Config{
		Image:       req.Image,
		Cmd:         append([]string(nil), req.Cmd...),
		Env:         append([]string(nil), req.Env...),
		StopTimeout: &timeout,
		Labels: map[string]string{
			IntentIDLabel: req.IntentID,
			JobIDLabel:    req.JobID,
			AttemptLabel:  strconv.Itoa(int(req.Attempt)),
		},
	}
	result, err := w.Client.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name:       req.Name,
		Config:     config,
		HostConfig: w.hostConfig(),
	})
	if err != nil {
		return "", lifecycleError(ctx, err)
	}
	if !fullContainerID.MatchString(result.ID) {
		return "", &creationError{
			outcome: CreationOutcomeUnknown,
			err:     terminalreason.Wrap(terminalreason.SystemError, status.Error(codes.Unavailable, "docker returned no full immutable container id")),
		}
	}
	return result.ID, nil
}

var fullContainerID = regexp.MustCompile("^[a-f0-9]{64}$")

// Start bounds mutation I/O without shortening the running workload's timeout.
func (w *DockerWorkflow) Start(ctx context.Context, timeout time.Duration, id string) (logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	if !fullContainerID.MatchString(id) || timeout <= 0 {
		return nil, nil, status.Error(codes.InvalidArgument, "start requires a full container id and positive timeout")
	}
	startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	_, err = w.Client.ContainerStart(startCtx, id, client.ContainerStartOptions{})
	if err != nil {
		err = lifecycleError(startCtx, err)
	}
	cancel()
	if err != nil {
		return nil, nil, fmt.Errorf("start container: %w", err)
	}
	logs, errs = w.watchContainer(ctx, timeout, id)
	return logs, errs, nil
}
