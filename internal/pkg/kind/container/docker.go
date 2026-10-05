package container

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
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
	containerStopTimeout      = 2 * time.Second
	dockerDialTimeout         = 10 * time.Second
	dockerTLSHandshakeTimeout = 10 * time.Second
	dockerHealthCheckTimeout  = 10 * time.Second
	dockerIdleConnTimeout     = 30 * time.Second
	dockerMaxIdleConns        = 6
	legacyDockerProxyPort     = "2375"
	dockerProxyTLSPort        = "2376"

	// dockerLogStreamStdout / dockerLogStreamStderr label published log lines.
	dockerLogStreamStdout = "stdout"
	dockerLogStreamStderr = "stderr"

	// dockerLogScanBufferBytes is the initial line-scan buffer; longer lines grow
	// it up to dockerLogScanMaxLineBytes instead of reallocating per line.
	dockerLogScanBufferBytes = 64 << 10

	// dockerLogScanMaxLineBytes caps a published log line at 256 KiB, four times
	// bufio.MaxScanTokenSize, past which Scanner would end the stream in silence;
	// a longer line is reported. Content bound only: escaping can enlarge the record.
	dockerLogScanMaxLineBytes = 256 << 10

	// maxContainerLogStreamErrors is how many failures the demuxer and the two
	// scanners can publish. Buffers that wide never block a producer, so neither
	// a broken stream nor an abandoned consumer can strand the channel owner;
	// Execute adds one slot for its terminal failure.
	maxContainerLogStreamErrors = 3

	// capDropAll drops every Linux capability from workload containers.
	capDropAll = "ALL"

	// workloadNetworkICCOption/Driver/ICCOff describe the dedicated workload
	// bridge: tenant containers on it cannot reach each other.
	workloadNetworkICCOption = "com.docker.network.bridge.enable_icc"
	workloadNetworkDriver    = "bridge"
	workloadNetworkICCOff    = "false"

	// workloadNetworkBridgeName pins the kernel interface for firewall -i matching.
	// Fixed at creation: resolving it at apply time would deadlock fresh installs.
	workloadNetworkBridgeName       = "chronoverse-br"
	workloadNetworkBridgeNameOption = "com.docker.network.bridge.name"

	// dockerProxyTokenEnv / dockerProxyTokenHeader carry the shared Kubernetes
	// socket-proxy credential without changing the persisted endpoint URL.
	dockerProxyTokenEnv    = "DOCKER_PROXY_TOKEN"               //nolint:gosec // Environment-variable name, not a credential.
	dockerProxyTokenHeader = "X-Chronoverse-Docker-Proxy-Token" //nolint:gosec // Header name, not a credential.

	// platformNetwork is the Compose service network; workloads must never attach.
	platformNetwork = "chronoverse"

	// DefaultWorkloadNetwork is the ICC-disabled bridge every workload is pinned
	// to; created on demand (VULN-004a/b).
	DefaultWorkloadNetwork = "chronoverse-workloads"

	// DefaultWorkloadSubnet pins the workload bridge to unrouted RFC 2544 space
	// so the fixed range never overlaps infra and the firewall can match it.
	DefaultWorkloadSubnet = "198.18.247.0/24"
)

// DockerProxyTLSConfig holds mTLS credentials for the per-node proxy on :2376.
// It is supplied via DockerWorkflowOption so callers can use the
// application's envconfig-based env management instead of direct os.Getenv
// checks inside the workflow package.
type DockerProxyTLSConfig struct {
	CAFile     string
	CertFile   string
	KeyFile    string
	ServerName string
}

// DockerWorkflow represents a Docker workflow.
type DockerWorkflow struct {
	*client.Client
	pullGroup        singleflight.Group
	resourceLimits   ResourceLimits
	dockerHost       string
	workloadNetwork  string
	workloadSubnet   string
	dockerProxyToken string
	dockerProxyTLS   DockerProxyTLSConfig
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

// WithWorkloadSubnet overrides the workload bridge CIDR; must match the host
// firewall rules. Empty keeps the default.
func WithWorkloadSubnet(cidr string) DockerWorkflowOption {
	return func(w *DockerWorkflow) {
		if cidr != "" {
			w.workloadSubnet = cidr
		}
	}
}

// WithDockerProxyTLS configures mTLS credentials for the per-node Docker
// proxy. The workload containers never receive these credentials; only the
// runtime-agent and worker processes use them.
func WithDockerProxyTLS(cfg DockerProxyTLSConfig) DockerWorkflowOption {
	return func(w *DockerWorkflow) {
		w.dockerProxyTLS = cfg
	}
}

// WithDockerProxyToken configures the shared bearer token for the Docker
// proxy. It is kept separate from the endpoint URL to avoid accidental
// disclosure in persisted job state.
func WithDockerProxyToken(token string) DockerWorkflowOption {
	return func(w *DockerWorkflow) {
		w.dockerProxyToken = token
	}
}

// newDockerProxyTLSConfig loads mTLS credentials for the per-node proxy on :2376.
func newDockerProxyTLSConfig(caFile, certFile, keyFile, serverName string) (*tls.Config, error) {
	if serverName == "" {
		return nil, errors.New("docker-proxy TLS server name is required")
	}
	if _, err := tls.LoadX509KeyPair(certFile, keyFile); err != nil {
		return nil, err
	}
	if _, err := loadDockerProxyCAPool(caFile); err != nil {
		return nil, err
	}

	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: serverName,
		// Verification is performed in VerifyConnection so the projected CA
		// bundle can rotate without restarting every cached Docker client.
		InsecureSkipVerify: true, //nolint:gosec // VerifyConnection performs hostname and chain verification with the current CA bundle.
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			cert, err := tls.LoadX509KeyPair(certFile, keyFile)
			if err != nil {
				return nil, err
			}
			return &cert, nil
		},
		VerifyConnection: func(state tls.ConnectionState) error {
			return verifyDockerProxyConnection(&state, caFile, serverName)
		},
	}, nil
}

func loadDockerProxyCAPool(caFile string) (*x509.CertPool, error) {
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("failed to append docker-proxy CA")
	}
	return caPool, nil
}

func verifyDockerProxyConnection(state *tls.ConnectionState, caFile, serverName string) error {
	if len(state.PeerCertificates) == 0 {
		return errors.New("docker-proxy did not present a server certificate")
	}
	roots, err := loadDockerProxyCAPool(caFile)
	if err != nil {
		return err
	}
	intermediates := x509.NewCertPool()
	for _, cert := range state.PeerCertificates[1:] {
		intermediates.AddCert(cert)
	}
	_, err = state.PeerCertificates[0].Verify(x509.VerifyOptions{
		DNSName:       serverName,
		Roots:         roots,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	return err
}

// NormalizeDockerProxyEndpoint migrates persisted plaintext proxy endpoints
// to the mTLS listener while preserving the exact runtime host. It is a no-op
// unless Docker proxy TLS is configured, so ordinary plaintext Docker daemons
// on port 2375 remain untouched.
func NormalizeDockerProxyEndpoint(endpoint string, tlsConfigured bool) string {
	if !tlsConfigured {
		return endpoint
	}
	const tcpPrefix = "tcp://"
	if !strings.HasPrefix(endpoint, tcpPrefix) {
		return endpoint
	}
	host, port, err := net.SplitHostPort(strings.TrimPrefix(endpoint, tcpPrefix))
	if err != nil || host == "" || port != legacyDockerProxyPort {
		return endpoint
	}
	return tcpPrefix + net.JoinHostPort(host, dockerProxyTLSPort)
}

// NewDockerWorkflow creates a new DockerWorkflow.
func NewDockerWorkflow(options ...DockerWorkflowOption) (*DockerWorkflow, error) {
	w := &DockerWorkflow{
		workloadNetwork:  DefaultWorkloadNetwork,
		workloadSubnet:   DefaultWorkloadSubnet,
		dockerProxyToken: os.Getenv(dockerProxyTokenEnv),
	}
	for _, option := range options {
		if option != nil {
			option(w)
		}
	}
	w.dockerHost = NormalizeDockerProxyEndpoint(w.dockerHost, w.dockerProxyTLS.CAFile != "")

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
	if w.dockerProxyTLS.CAFile != "" {
		if w.dockerProxyTLS.CertFile == "" || w.dockerProxyTLS.KeyFile == "" {
			return nil, status.Errorf(codes.Internal, "docker proxy TLS misconfigured: CA file set but cert/key missing")
		}
		tlsConfig, err := newDockerProxyTLSConfig(w.dockerProxyTLS.CAFile, w.dockerProxyTLS.CertFile, w.dockerProxyTLS.KeyFile, w.dockerProxyTLS.ServerName)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to load docker proxy TLS config: %v", err)
		}
		transport := &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   dockerDialTimeout,
				KeepAlive: dockerIdleConnTimeout,
			}).DialContext,
			MaxIdleConns:        dockerMaxIdleConns,
			IdleConnTimeout:     dockerIdleConnTimeout,
			TLSHandshakeTimeout: dockerTLSHandshakeTimeout,
			TLSClientConfig:     tlsConfig,
		}
		httpClient := &http.Client{
			Transport:     transport,
			CheckRedirect: client.CheckRedirect,
		}
		clientOptions = append(clientOptions, client.WithHTTPClient(httpClient))
	}
	cli, err := client.NewClientWithOpts(
		clientOptions...,
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to initialize docker client: %v", err)
	}

	w.Client = cli

	healthCtx, cancelHealth := context.WithTimeout(context.Background(), dockerHealthCheckTimeout)
	defer cancelHealth()
	if err := w.healthCheck(healthCtx); err != nil {
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
		return w.validateWorkloadNetwork(w.workloadNetwork, &inspected)
	} else if !cerrdefs.IsNotFound(err) {
		return status.Errorf(codes.Internal, "failed to inspect workload network %q: %v", w.workloadNetwork, err)
	}

	if _, err := w.Client.NetworkCreate(ctx, w.workloadNetwork, network.CreateOptions{
		Driver: workloadNetworkDriver,
		Options: map[string]string{
			// Tenant containers on this network must not reach each other.
			workloadNetworkICCOption: workloadNetworkICCOff,
			// Pinned interface name for firewall -i matching.
			workloadNetworkBridgeNameOption: workloadNetworkBridgeName,
		},
		IPAM: &network.IPAM{
			Config: []network.IPAMConfig{{Subnet: w.workloadSubnet}},
		},
	}); err != nil {
		// Lost a create race (or the daemon reported the conflict with an
		// inconsistent status): accept the winner only if it is isolated.
		inspected, inspectErr := w.Client.NetworkInspect(ctx, w.workloadNetwork, network.InspectOptions{})
		if inspectErr != nil {
			return status.Errorf(codes.Internal, "failed to create workload network %q: %v", w.workloadNetwork, err)
		}
		return w.validateWorkloadNetwork(w.workloadNetwork, &inspected)
	}

	// Verify the daemon honored the requested options (fail-closed).
	inspected, err := w.Client.NetworkInspect(ctx, w.workloadNetwork, network.InspectOptions{})
	if err != nil {
		return status.Errorf(codes.Internal, "failed to verify workload network %q after creation: %v", w.workloadNetwork, err)
	}
	return w.validateWorkloadNetwork(w.workloadNetwork, &inspected)
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

func (w *DockerWorkflow) validateWorkloadNetwork(configuredName string, inspected *network.Inspect) error {
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
	if inspected.Options[workloadNetworkBridgeNameOption] != workloadNetworkBridgeName {
		return status.Errorf(codes.FailedPrecondition, "workload network %q has unexpected bridge interface (firewall -i match would miss)", configuredName)
	}
	// Refuse subnets the firewall doesn't cover instead of silently bypassing it.
	if _, _, err := net.ParseCIDR(w.workloadSubnet); err != nil {
		return status.Errorf(codes.FailedPrecondition, "workload subnet %q is invalid: %v", w.workloadSubnet, err)
	}
	// Exactly one IPAM range — the enforced subnet — and no IPv6: extra ranges
	// or a dual-stack pool would hand out addresses the firewall doesn't cover.
	if inspected.EnableIPv6 {
		return status.Errorf(codes.FailedPrecondition, "workload network %q must not enable IPv6", configuredName)
	}
	if len(inspected.IPAM.Config) != 1 || inspected.IPAM.Config[0].Subnet != w.workloadSubnet {
		return status.Errorf(codes.FailedPrecondition, "workload network %q is not exactly on enforced subnet %q", configuredName, w.workloadSubnet)
	}
	return nil
}

func (w *DockerWorkflow) healthCheck(ctx context.Context) error {
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

	if err := w.Client.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return containerID, nil, nil, status.Errorf(codes.Aborted, "failed to start container: %v", err)
	}

	logs := make(chan *jobsmodel.JobLog)
	// Sized for every stream failure plus one terminal result, so neither blocks
	// on a consumer that stopped draining. The terminal result is sent
	// unconditionally: a caller that canceled still needs to learn why it ended.
	errs := make(chan error, maxContainerLogStreamErrors+1)

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)

	go func() { //nolint:gosec // Execution must remain tied to the caller context so cancellation stops the container.
		defer close(logs)
		defer close(errs)
		defer cancel()

		statusCh, waitErrCh := w.Client.ContainerWait(timeoutCtx, containerID, container.WaitConditionNotRunning)

		logsDone := make(chan struct{})
		go func() {
			defer close(logsDone)
			w.streamContainerLogs(timeoutCtx, containerID, logs, errs, true)
		}()

		// The stream goroutine is the only other publisher on logs and errs, so it
		// must be finished before the deferred closes run; canceling first bounds
		// the join by aborting the daemon read.
		stopLogStream := func() {
			cancel()
			<-logsDone
		}

		select {
		case <-timeoutCtx.Done():
			if errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
				errs <- terminalreason.Wrap(terminalreason.TimeLimitExceeded, status.Errorf(codes.DeadlineExceeded, "container execution timed out: %v", timeoutCtx.Err()))
			} else {
				errs <- status.Errorf(codes.Canceled, "container execution canceled: %v", timeoutCtx.Err())
			}

			stopTimeout := int(containerStopTimeout.Seconds())
			stopCtx, stopCancel := context.WithTimeout(context.Background(), dockerHealthCheckTimeout)
			//nolint:errcheck,contextcheck // Ignore error, as we are trying to stop the container gracefully.
			_ = w.Client.ContainerStop(stopCtx, containerID, container.StopOptions{
				Timeout: &stopTimeout,
			})
			stopCancel()

			stopLogStream()
			return

		case err := <-waitErrCh:
			if strings.Contains(err.Error(), "No such container") {
				stopLogStream()
				return
			}

			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				errs <- terminalreason.Wrap(terminalreason.TimeLimitExceeded, status.Errorf(codes.DeadlineExceeded, "container execution timed out: %v", ctx.Err()))
			} else if errors.Is(ctx.Err(), context.Canceled) {
				errs <- status.Errorf(codes.Canceled, "container execution canceled: %v", ctx.Err())
			} else {
				errs <- status.Errorf(codes.Aborted, "container execution error: %v", err)
			}

			stopLogStream()
			return

		case containerStatus := <-statusCh:
			// The daemon ends the log stream once the container exits, so
			// wait for it here and let the deferred cancel follow the drain.
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
	// Wide enough for every stream failure the demuxer and both scanners
	// can publish, so the forwarder never blocks on its own errors.
	errsCh := make(chan error, maxContainerLogStreamErrors)

	go func() {
		defer close(logsCh)
		defer close(errsCh)

		w.streamContainerLogs(ctx, containerID, logsCh, errsCh, false)
	}()

	return logsCh, errsCh, nil
}

// containerLogLine is one demuxed container log line awaiting publication.
type containerLogLine struct {
	stream  string
	message string
}

// streamContainerLogs streams container logs and properly demuxes stdout/stderr.
//
// It is the only allocator of sequence numbers, so every line gets a unique,
// contiguous, zero-based sequence however the two streams interleave. Every send
// selects on ctx and the daemon read is aborted on ctx, so it always returns:
// callers that own the channels may join it before closing them.
func (w *DockerWorkflow) streamContainerLogs(ctx context.Context, containerID string, logCh chan<- *jobsmodel.JobLog, errs chan<- error, follow bool) {
	reader, err := w.Client.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
	})
	if err != nil {
		publishContainerLogError(ctx, errs, containerLogRequestError(err))
		return
	}
	defer reader.Close()

	// Abort the daemon body read on cancellation instead of waiting it out.
	stopReaderOnCancel := context.AfterFunc(ctx, func() {
		_ = reader.Close()
	})
	defer stopReaderOnCancel()

	stdoutReader, stdoutWriter := io.Pipe()
	stderrReader, stderrWriter := io.Pipe()
	// Closing the readers releases a demuxer write, so an early-stopping
	// scanner cannot strand the writer.
	defer stdoutReader.Close()
	defer stderrReader.Close()

	lines := make(chan containerLogLine)
	streamErrs := make(chan error, maxContainerLogStreamErrors)

	var producers sync.WaitGroup
	producers.Go(func() {
		defer stdoutWriter.Close()
		defer stderrWriter.Close()

		if _, err := stdcopy.StdCopy(stdoutWriter, stderrWriter, reader); err != nil {
			publishContainerLogError(ctx, streamErrs, containerLogReadError(ctx, err))
		}
	})
	producers.Go(func() {
		scanContainerLogStream(ctx, stdoutReader, dockerLogStreamStdout, lines, streamErrs)
	})
	producers.Go(func() {
		scanContainerLogStream(ctx, stderrReader, dockerLogStreamStderr, lines, streamErrs)
	})
	// Closed only once every producer returned, so no send can race the close.
	go func() {
		producers.Wait()
		close(lines)
		close(streamErrs)
	}()

	var sequenceNum uint32
	for lines != nil || streamErrs != nil {
		select {
		case line, ok := <-lines:
			if !ok {
				lines = nil
				continue
			}

			select {
			case logCh <- &jobsmodel.JobLog{
				Timestamp:   time.Now(),
				Message:     line.message,
				SequenceNum: sequenceNum,
				Stream:      line.stream,
			}:
				sequenceNum++
			case <-ctx.Done():
				return
			}
		case streamErr, ok := <-streamErrs:
			if !ok {
				streamErrs = nil
				continue
			}
			publishContainerLogError(ctx, errs, streamErr)
		case <-ctx.Done():
			return
		}
	}
}

// scanContainerLogStream publishes every nonblank line of one demuxed stream.
// The scanner error is published, not discarded: an unreadable line must surface
// as a failure rather than end the stream in silence.
func scanContainerLogStream(ctx context.Context, stream *io.PipeReader, name string, lines chan<- containerLogLine, errs chan<- error) {
	defer stream.Close()

	scanner := bufio.NewScanner(stream)
	// Room for the cap plus the longest terminator bufio can meet (bare LF, or a
	// CRLF whose CR it strips), which it counts against its maximum. Tokens over
	// the cap are rejected below: an unterminated EOF line has no terminator.
	scanner.Buffer(make([]byte, 0, dockerLogScanBufferBytes), dockerLogScanMaxLineBytes+2)

	for scanner.Scan() {
		message := scanner.Text()
		if len(message) > dockerLogScanMaxLineBytes {
			publishContainerLogError(ctx, errs, containerLogOversizedLineError(name))
			return
		}

		// A blank line carries nothing and must not consume a sequence number.
		if message == "" {
			continue
		}

		select {
		case lines <- containerLogLine{stream: name, message: message}:
		case <-ctx.Done():
			return
		}
	}

	if err := scanner.Err(); err != nil {
		publishContainerLogError(ctx, errs, containerLogScanError(name, err))
	}
}

// publishContainerLogError delivers a stream failure unless ctx is already done,
// checked first so cancellation cannot race the send into a ready buffer and turn
// a caller-closed reader into a reported failure. The select covers cancellation
// arriving mid-publication, and a blocked send stays the fallback for a caller that
// abandoned the channel, so the publisher can always be joined.
func publishContainerLogError(ctx context.Context, errs chan<- error, err error) {
	if err == nil || ctx.Err() != nil {
		return
	}

	select {
	case errs <- err:
	case <-ctx.Done():
	}
}

// containerLogRequestError classifies a failure to open the daemon log stream.
func containerLogRequestError(err error) error {
	switch {
	case cerrdefs.IsNotFound(err):
		return status.Errorf(codes.NotFound, "container not found: %v", err)
	case client.IsErrConnectionFailed(err):
		return status.Errorf(codes.Unavailable, "docker daemon unavailable: %v", err)
	default:
		return status.Errorf(codes.Aborted, "failed to get container logs: %v", err)
	}
}

// containerLogReadError classifies a demultiplexing failure, returning nil when
// the stream simply ended.
func containerLogReadError(ctx context.Context, err error) error {
	if isContainerLogStreamEnd(ctx, err) {
		return nil
	}

	if client.IsErrConnectionFailed(err) {
		return status.Errorf(codes.Unavailable, "docker daemon unavailable: %v", err)
	}

	return status.Errorf(codes.Aborted, "failed to read container logs: %v", err)
}

// isContainerLogStreamEnd reports whether the demuxer stopped because the stream
// or the caller's context ended, neither of which is a failure to report.
func isContainerLogStreamEnd(ctx context.Context, err error) bool {
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrClosedPipe) ||
		ctx.Err() != nil ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}

// containerLogScanError classifies a line-scanning failure.
func containerLogScanError(stream string, err error) error {
	if errors.Is(err, bufio.ErrTooLong) {
		return containerLogOversizedLineError(stream)
	}

	return status.Errorf(codes.Aborted, "failed to read container logs: %s stream: %v", stream, err)
}

// containerLogOversizedLineError reports a line past the content cap and names
// it, so a truncated run stays distinguishable from a quiet one.
func containerLogOversizedLineError(stream string) error {
	return status.Errorf(codes.Aborted, "failed to read container logs: %s line exceeds the %d byte limit", stream, dockerLogScanMaxLineBytes)
}

// Build pulls an image from the registry, required for the image to be available locally.
func (w *DockerWorkflow) Build(ctx context.Context, imageName string) error {
	if err := w.healthCheck(ctx); err != nil {
		return err
	}

	resultCh := w.pullGroup.DoChan(imageName, func() (any, error) {
		if _, err := w.Client.ImageInspect(ctx, imageName); err == nil {
			return struct{}{}, nil
		} else if !cerrdefs.IsNotFound(err) {
			return nil, dockerImageInspectError(err)
		}

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
