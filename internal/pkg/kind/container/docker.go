package container

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/distribution/reference"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"golang.org/x/sync/singleflight"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/terminalreason"
)

const (
	// containerStopTimeout is the default timeout for stopping a container.
	containerStopTimeout = 2 * time.Second

	// capDropAll drops every Linux capability from workload containers.
	capDropAll = "ALL"

	// workloadNetworkICCOption/Driver/ICCOff describe the dedicated workload
	// bridge: tenant containers on it cannot reach each other.
	workloadNetworkICCOption = "com.docker.network.bridge.enable_icc"
	workloadNetworkDriver    = "bridge"
	workloadNetworkICCOff    = "false"

	// dockerProxyTokenEnv / dockerProxyTokenHeader carry the shared Kubernetes
	// socket-proxy credential without changing the persisted endpoint URL.
	dockerProxyTokenEnv    = "DOCKER_PROXY_TOKEN"               //nolint:gosec // Environment-variable name, not a credential.
	dockerProxyTokenHeader = "X-Chronoverse-Docker-Proxy-Token" //nolint:gosec // Header name, not a credential.

	// platformNetwork is the Compose service network; workloads must never attach.
	platformNetwork = "chronoverse"

	// DefaultWorkloadNetwork is the ICC-disabled bridge every workload is pinned
	// to; created on demand (VULN-004a/b).
	DefaultWorkloadNetwork = "chronoverse-workloads"
)

// DockerWorkflow represents a Docker workflow.
type DockerWorkflow struct {
	*client.Client
	pullGroup        singleflight.Group
	resourceLimits   ResourceLimits
	dockerHost       string
	workloadNetwork  string
	dockerProxyToken string
}

// ResourceLimits defines Docker resource limits applied to executed workload containers.
type ResourceLimits struct {
	MemoryBytes int64
	NanoCPUs    int64
	PidsLimit   int64
}

// State represents the observed state of a Docker container.
type State struct {
	Running  bool
	ExitCode int
	Status   string
}

// DockerWorkflowOption configures a DockerWorkflow.
type DockerWorkflowOption func(*DockerWorkflow)

// WithResourceLimits configures resource limits for executed workload containers.
func WithResourceLimits(limits ResourceLimits) DockerWorkflowOption {
	return func(w *DockerWorkflow) {
		w.resourceLimits = limits
	}
}

// WithDockerHost configures the Docker daemon endpoint for this workflow.
func WithDockerHost(host string) DockerWorkflowOption {
	return func(w *DockerWorkflow) {
		w.dockerHost = host
	}
}

// WithWorkloadNetwork overrides the docker network workload containers are
// attached to. The network is created on demand if the daemon does not have
// it yet.
func WithWorkloadNetwork(name string) DockerWorkflowOption {
	return func(w *DockerWorkflow) {
		if name != "" {
			w.workloadNetwork = name
		}
	}
}

// NewDockerWorkflow creates a new DockerWorkflow.
func NewDockerWorkflow(options ...DockerWorkflowOption) (*DockerWorkflow, error) {
	w := &DockerWorkflow{
		workloadNetwork:  DefaultWorkloadNetwork,
		dockerProxyToken: os.Getenv(dockerProxyTokenEnv),
	}
	for _, option := range options {
		if option != nil {
			option(w)
		}
	}

	clientOptions := []client.Opt{
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	}
	if w.dockerHost != "" {
		clientOptions = append(clientOptions, client.WithHost(w.dockerHost))
	}
	if w.dockerProxyToken != "" {
		clientOptions = append(clientOptions, client.WithHTTPHeaders(map[string]string{
			dockerProxyTokenHeader: w.dockerProxyToken,
		}))
	}
	cli, err := client.NewClientWithOpts(
		clientOptions...,
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to initialize docker client: %v", err)
	}

	w.Client = cli

	if err := w.healthCheck(context.Background()); err != nil {
		// EndpointCache retries failed constructions per claim: close the ping
		// connection or persistent faults leak one connection per attempt.
		cli.Close()
		return nil, err
	}

	// No network work here: lifecycle-only clients (expired-lease recovery)
	// must construct even when the workload network is broken. Execute enforces
	// isolation before every container creation.

	return w, nil
}

// workloadNetworkMissing reports whether the configured network is absent;
// other inspect errors return false so the caller keeps its original failure.
func (w *DockerWorkflow) workloadNetworkMissing(ctx context.Context) bool {
	_, err := w.Client.NetworkInspect(ctx, w.workloadNetwork, network.InspectOptions{})
	return cerrdefs.IsNotFound(err)
}

// ensureWorkloadNetwork idempotently creates the dedicated workload network
// (bridge, ICC disabled) when missing, then validates what the daemon created.
func (w *DockerWorkflow) ensureWorkloadNetwork(ctx context.Context) error {
	if err := validateWorkloadNetworkName(w.workloadNetwork); err != nil {
		return err
	}

	if inspected, err := w.Client.NetworkInspect(ctx, w.workloadNetwork, network.InspectOptions{}); err == nil {
		return validateWorkloadNetwork(w.workloadNetwork, &inspected)
	} else if !cerrdefs.IsNotFound(err) {
		return status.Errorf(codes.Internal, "failed to inspect workload network %q: %v", w.workloadNetwork, err)
	}

	if _, err := w.Client.NetworkCreate(ctx, w.workloadNetwork, network.CreateOptions{
		Driver: workloadNetworkDriver,
		Options: map[string]string{
			// Tenant containers on this network must not reach each other.
			workloadNetworkICCOption: workloadNetworkICCOff,
		},
	}); err != nil {
		// Lost a create race (or the daemon reported the conflict with an
		// inconsistent status): accept the winner only if it is isolated.
		inspected, inspectErr := w.Client.NetworkInspect(ctx, w.workloadNetwork, network.InspectOptions{})
		if inspectErr != nil {
			return status.Errorf(codes.Internal, "failed to create workload network %q: %v", w.workloadNetwork, err)
		}
		return validateWorkloadNetwork(w.workloadNetwork, &inspected)
	}

	// Verify the daemon honored the requested options (fail-closed).
	inspected, err := w.Client.NetworkInspect(ctx, w.workloadNetwork, network.InspectOptions{})
	if err != nil {
		return status.Errorf(codes.Internal, "failed to verify workload network %q after creation: %v", w.workloadNetwork, err)
	}
	return validateWorkloadNetwork(w.workloadNetwork, &inspected)
}

// workloadNetworkNamePattern is the character class the k8s socket-proxy ACL
// accepts for network paths, so configured names stay portable between direct
// Docker and proxied Kubernetes daemons.
var workloadNetworkNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func validateWorkloadNetworkName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || trimmed != name {
		return status.Errorf(codes.FailedPrecondition, "workload network name %q is invalid", name)
	}
	normalized := strings.ToLower(trimmed)

	switch normalized {
	case network.NetworkDefault, network.NetworkHost, network.NetworkNone, network.NetworkBridge, network.NetworkNat, platformNetwork, "container":
		return status.Errorf(codes.FailedPrecondition, "workload network %q is reserved", name)
	}
	if strings.HasPrefix(normalized, "container:") {
		return status.Errorf(codes.FailedPrecondition, "workload network %q is a reserved container network mode", name)
	}
	if !workloadNetworkNamePattern.MatchString(trimmed) {
		return status.Errorf(codes.FailedPrecondition, "workload network name %q is invalid", name)
	}

	return nil
}

func validateWorkloadNetwork(configuredName string, inspected *network.Inspect) error {
	if err := validateWorkloadNetworkName(inspected.Name); err != nil {
		return status.Errorf(codes.FailedPrecondition, "workload network %q resolved to an unsafe network: %v", configuredName, err)
	}
	if inspected.Name != configuredName {
		return status.Errorf(codes.FailedPrecondition, "workload network %q resolved to unexpected network %q", configuredName, inspected.Name)
	}
	if inspected.Driver != workloadNetworkDriver {
		return status.Errorf(codes.FailedPrecondition, "workload network %q uses driver %q, want bridge", configuredName, inspected.Driver)
	}
	if inspected.Options[workloadNetworkICCOption] != workloadNetworkICCOff {
		return status.Errorf(codes.FailedPrecondition, "workload network %q does not disable inter-container communication", configuredName)
	}

	return nil
}

func (w *DockerWorkflow) healthCheck(ctx context.Context) error {
	// Health check the Docker client
	if _, err := w.Client.Ping(ctx); err != nil {
		return status.Errorf(codes.Internal, "failed to ping docker client: %v", err)
	}

	return nil
}

// Healthy checks whether the configured Docker daemon is reachable.
func (w *DockerWorkflow) Healthy(ctx context.Context) error {
	return w.healthCheck(ctx)
}

// DockerHost returns the Docker daemon host configured for this client.
func (w *DockerWorkflow) DockerHost() string {
	return w.Client.DaemonHost()
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

	createContainer := func() (container.CreateResponse, error) {
		return w.Client.ContainerCreate(
			ctx,
			&container.Config{
				Image:       image,
				Cmd:         cmd,
				StopTimeout: &containerTimeout,
				Env:         env,
			},
			w.hostConfig(),
			nil, nil, "",
		)
	}

	// Cached clients outlive the network they initialized: revalidate before
	// use so prunes are recreated and unsafe replacements are rejected before
	// Docker attaches a workload. Drift is infra, not user error, hence the
	// retryable Internal classification.
	if err := w.ensureWorkloadNetwork(ctx); err != nil {
		return "", nil, nil, status.Errorf(codes.Internal, "workload network is not ready: %v", err)
	}

	// Create container with auto-removal
	resp, err := createContainer()
	if err != nil && w.workloadNetworkMissing(ctx) {
		// The network was pruned between the check and the create; a missing
		// network proves no container was created, so one retry is safe.
		if ensureErr := w.ensureWorkloadNetwork(ctx); ensureErr != nil {
			return "", nil, nil, status.Errorf(codes.Internal, "workload network is not ready: %v", ensureErr)
		}
		resp, err = createContainer()
	}
	if err != nil || resp.ID == "" {
		return "", nil, nil, status.Errorf(codes.FailedPrecondition, "failed to create container: %v", err)
	}

	containerID := resp.ID

	// Start the container
	if err := w.Client.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return containerID, nil, nil, status.Errorf(codes.Aborted, "failed to start container: %v", err)
	}

	// Channel for logs streaming
	logs := make(chan *jobsmodel.JobLog)
	// Channel to capture errors
	errs := make(chan error)

	// Create a context with timeout for this container
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)

	// Stream logs and handle container completion
	go func() { //nolint:gosec // Execution must remain tied to the caller context so cancellation stops the container.
		defer close(logs)
		defer close(errs)
		defer cancel()

		// Set up container wait early to detect completion
		statusCh, waitErrCh := w.Client.ContainerWait(timeoutCtx, containerID, container.WaitConditionNotRunning)

		// Start log streaming
		logsDone := make(chan struct{})
		go func() {
			defer close(logsDone)
			w.streamContainerLogs(timeoutCtx, containerID, logs, errs, true)
		}()

		// Monitor for timeouts and container completion
		select {
		case <-timeoutCtx.Done():
			if errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
				errs <- terminalreason.Wrap(terminalreason.TimeLimitExceeded, status.Errorf(codes.DeadlineExceeded, "container execution timed out: %v", timeoutCtx.Err()))
			} else {
				errs <- status.Errorf(codes.Canceled, "container execution canceled: %v", timeoutCtx.Err())
			}

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
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				errs <- terminalreason.Wrap(terminalreason.TimeLimitExceeded, status.Errorf(codes.DeadlineExceeded, "container execution timed out: %v", ctx.Err()))
			} else if errors.Is(ctx.Err(), context.Canceled) {
				errs <- status.Errorf(codes.Canceled, "container execution canceled: %v", ctx.Err())
			} else {
				errs <- status.Errorf(codes.Aborted, "container execution error: %v", err)
			}

		case containerStatus := <-statusCh:
			// Check exit code after logs finish
			<-logsDone

			if containerStatus.StatusCode != 0 {
				errs <- terminalreason.Wrap(terminalreason.NonZeroExit, status.Errorf(codes.Aborted, "container exited with non-zero code: %d", containerStatus.StatusCode))
			}
		}
	}()

	return containerID, logs, errs, nil
}

func (w *DockerWorkflow) hostConfig() *container.HostConfig {
	// Workload isolation (VULN-004a): dedicated network, no capabilities, no
	// privilege escalation, read-only rootfs with a writable /tmp.
	networkMode := DefaultWorkloadNetwork
	if w != nil && w.workloadNetwork != "" {
		networkMode = w.workloadNetwork
	}

	hostConfig := &container.HostConfig{
		AutoRemove:     false,
		NetworkMode:    container.NetworkMode(networkMode),
		CapDrop:        []string{capDropAll},
		SecurityOpt:    []string{"no-new-privileges"},
		ReadonlyRootfs: true,
		Tmpfs: map[string]string{
			"/tmp": "rw,nosuid,size=256m",
		},
		IpcMode: container.IPCModePrivate,
	}
	if w == nil {
		return hostConfig
	}

	resources := container.Resources{}
	if w.resourceLimits.MemoryBytes > 0 {
		resources.Memory = w.resourceLimits.MemoryBytes
	}
	if w.resourceLimits.NanoCPUs > 0 {
		resources.NanoCPUs = w.resourceLimits.NanoCPUs
	}
	if w.resourceLimits.PidsLimit > 0 {
		pidsLimit := w.resourceLimits.PidsLimit
		resources.PidsLimit = &pidsLimit
	}
	hostConfig.Resources = resources

	return hostConfig
}

// Logs replays the retained logs for a container.
func (w *DockerWorkflow) Logs(ctx context.Context, containerID string) (logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	if healthErr := w.healthCheck(ctx); healthErr != nil {
		return nil, nil, healthErr
	}

	logsCh := make(chan *jobsmodel.JobLog)
	errsCh := make(chan error, 1)

	go func() {
		defer close(logsCh)
		defer close(errsCh)

		w.streamContainerLogs(ctx, containerID, logsCh, errsCh, false)
	}()

	return logsCh, errsCh, nil
}

// streamContainerLogs streams container logs and properly demuxes stdout/stderr.
//
//nolint:gocyclo // This function is not complex enough to warrant a refactor
func (w *DockerWorkflow) streamContainerLogs(ctx context.Context, containerID string, logCh chan<- *jobsmodel.JobLog, errs chan<- error, follow bool) {
	reader, err := w.Client.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
	})
	if err != nil {
		// To distinguish between Docker daemon unavailability and other errors
		switch {
		case cerrdefs.IsNotFound(err):
			errs <- status.Errorf(codes.NotFound, "container not found: %v", err)
		case client.IsErrConnectionFailed(err):
			errs <- status.Errorf(codes.Unavailable, "docker daemon unavailable: %v", err)
		default:
			errs <- status.Errorf(codes.Aborted, "failed to get container logs: %v", err)
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
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}
			if client.IsErrConnectionFailed(err) {
				errs <- status.Errorf(codes.Unavailable, "docker daemon unavailable: %v", err)
			} else {
				errs <- status.Errorf(codes.Aborted, "failed to read container logs: %v", err)
			}
		}
	}()

	// Wait group to track when both readers are done
	var wg sync.WaitGroup

	// Read from stdout
	wg.Go(func() {
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
	})

	// Read from stderr
	wg.Go(func() {
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
	})

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

	resultCh := w.pullGroup.DoChan(imageName, func() (any, error) {
		if _, err := w.Client.ImageInspect(ctx, imageName); err == nil {
			// Image already exists locally, no need to pull
			return struct{}{}, nil
		} else if !cerrdefs.IsNotFound(err) {
			// An error other than "not found" occurred
			return nil, dockerImageInspectError(err)
		}

		// Pull the image since it doesn't exist locally
		out, err := w.Client.ImagePull(ctx, imageName, image.PullOptions{})
		if err != nil {
			return nil, terminalreason.Wrap(terminalreason.ImagePullFailed, status.Errorf(codes.NotFound, "failed to pull image: %v", err))
		}
		defer out.Close()

		// Read the output to completion so the pulled image is registered locally.
		if _, err = io.Copy(io.Discard, out); err != nil {
			return nil, terminalreason.Wrap(terminalreason.ImagePullFailed, status.Errorf(codes.Aborted, "failed to read image pull output: %v", err))
		}

		return struct{}{}, nil
	})

	select {
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return status.Error(codes.DeadlineExceeded, ctx.Err().Error())
		}

		return status.Error(codes.Canceled, ctx.Err().Error())
	case result := <-resultCh:
		return result.Err
	}
}

// ImageExists reports whether an image is already available in the local Docker daemon.
func (w *DockerWorkflow) ImageExists(ctx context.Context, imageName string) (bool, error) {
	if err := w.healthCheck(ctx); err != nil {
		return false, err
	}

	if _, err := w.Client.ImageInspect(ctx, imageName); err == nil {
		return true, nil
	} else if !cerrdefs.IsNotFound(err) {
		return false, dockerImageInspectError(err)
	}

	return false, nil
}

// ResolveImageDigest ensures an image can be resolved and returns an immutable image reference.
func (w *DockerWorkflow) ResolveImageDigest(ctx context.Context, imageName string) (resolvedImageRef, resolvedImageDigest string, err error) {
	alreadyDigested := strings.Contains(imageName, "@sha256:")
	if buildErr := w.Build(ctx, imageName); buildErr != nil {
		return imageName, "", buildErr
	}
	inspect, err := w.Client.ImageInspect(ctx, imageName)
	if err != nil {
		return imageName, "", dockerImageInspectError(err)
	}
	if alreadyDigested {
		return imageName, imageName, nil
	}
	resolvedDigest, err := matchingRepositoryDigest(imageName, inspect.RepoDigests)
	if err != nil {
		return imageName, "", err
	}
	return imageName, resolvedDigest, nil
}

func matchingRepositoryDigest(imageName string, repoDigests []string) (string, error) {
	requestedRef, err := reference.ParseNormalizedNamed(imageName)
	if err != nil {
		return "", status.Errorf(codes.InvalidArgument, "invalid image reference %s: %v", imageName, err)
	}
	requestedRepo := reference.TrimNamed(requestedRef).Name()

	for _, repoDigest := range repoDigests {
		if repoDigest == "" {
			continue
		}
		candidateRef, err := reference.ParseNormalizedNamed(repoDigest)
		if err != nil {
			continue
		}
		if reference.TrimNamed(candidateRef).Name() == requestedRepo {
			return repoDigest, nil
		}
	}
	return "", status.Errorf(codes.FailedPrecondition, "image %s has no matching repository digest; use a registry-pullable image", imageName)
}

func dockerImageInspectError(err error) error {
	code := codes.Aborted
	switch {
	case cerrdefs.IsInvalidArgument(err):
		code = codes.InvalidArgument
	case cerrdefs.IsUnavailable(err):
		code = codes.Unavailable
	case cerrdefs.IsDeadlineExceeded(err):
		code = codes.DeadlineExceeded
	case cerrdefs.IsCanceled(err):
		code = codes.Canceled
	case cerrdefs.IsInternal(err):
		code = codes.Internal
	}

	return status.Errorf(code, "failed to inspect image: %v", err)
}

// Inspect returns the current Docker state for a container.
func (w *DockerWorkflow) Inspect(ctx context.Context, containerID string) (*State, error) {
	if err := w.healthCheck(ctx); err != nil {
		return nil, err
	}

	data, err := w.Client.ContainerInspect(ctx, containerID)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil, status.Errorf(codes.NotFound, "container not found: %v", err)
		}
		if client.IsErrConnectionFailed(err) {
			return nil, status.Errorf(codes.Unavailable, "docker daemon unavailable: %v", err)
		}
		return nil, status.Errorf(codes.Aborted, "failed to inspect container %s: %v", containerID, err)
	}
	if data.State == nil {
		return nil, status.Errorf(codes.Aborted, "container %s has no state", containerID)
	}

	return &State{
		Running:  data.State.Running,
		ExitCode: data.State.ExitCode,
		Status:   data.State.Status,
	}, nil
}

// Remove deletes a stopped container and ignores containers that are already gone.
func (w *DockerWorkflow) Remove(ctx context.Context, containerID string) error {
	if err := w.Client.ContainerRemove(ctx, containerID, container.RemoveOptions{
		RemoveVolumes: true,
		Force:         true,
	}); err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil
		}
		if isContainerRemovalInProgress(err) {
			return nil
		}
		if client.IsErrConnectionFailed(err) {
			return status.Errorf(codes.Unavailable, "docker daemon unavailable: %v", err)
		}
		return status.Errorf(codes.Aborted, "failed to remove container %s: %v", containerID, err)
	}

	return nil
}

func isContainerRemovalInProgress(err error) bool {
	return err != nil &&
		strings.Contains(err.Error(), "removal of container") &&
		strings.Contains(err.Error(), "is already in progress")
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
		if cerrdefs.IsNotFound(err) {
			return nil
		}
		if client.IsErrConnectionFailed(err) {
			return status.Errorf(codes.Unavailable, "docker daemon unavailable: %v", err)
		}
		return status.Errorf(codes.Aborted, "failed to stop container %s: %v", containerID, err)
	}

	return nil
}
