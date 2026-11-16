//nolint:testpackage // Reuses the existing unexported Docker proxy certificate fixtures.
package container

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dockertypes "github.com/moby/moby/api/types/container"
	dockerclient "github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.yaml.in/yaml/v3"
)

const (
	dockerProxyLifecycleImage = "haproxy:3.4.7-alpine3.24"
	dockerProxyLifecycleToken = "integration-docker-proxy-token"
	dockerProxyLifecycleHost  = "host.docker.internal"
	dockerProxyLifecyclePort  = "2376/tcp"
)

type dockerProxyLifecycleConfig struct {
	name    string
	content string
}

type dockerProxyLifecycleCase struct {
	name    string
	method  string
	path    string
	status  int
	backend string
}

// dockerProxyLifecycleConfigs returns both deployed proxy configurations: the
// compose bind mount and the config Map embedded in the Kubernetes manifest.
func dockerProxyLifecycleConfigs(t *testing.T) []dockerProxyLifecycleConfig {
	t.Helper()

	root := dockerProxyLifecycleRepoRoot(t)
	compose, err := os.ReadFile(filepath.Join(root, "compose", "haproxy", "haproxy.cfg"))
	require.NoError(t, err)

	manifest, err := os.Open(filepath.Join(root, "infra", "k8s", "base", "docker-proxy.yaml"))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = manifest.Close()
	})

	decoder := yaml.NewDecoder(manifest)
	var kubernetes string
	for {
		var document struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
			Data map[string]string `yaml:"data"`
		}
		err := decoder.Decode(&document)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		if document.Kind == "ConfigMap" && document.Metadata.Name == "docker-proxy-config" {
			kubernetes = document.Data["haproxy.cfg"]
		}
	}
	require.NotEmpty(t, kubernetes, "Kubernetes docker-proxy ConfigMap is missing its HAProxy config")

	return []dockerProxyLifecycleConfig{
		{name: "compose", content: string(compose)},
		{name: "kubernetes", content: kubernetes},
	}
}

// testkit imports this package, so this test package cannot import testkit for
// its Docker preflight. Mirror the same short-skip and strict-mode behavior.
func dockerProxyLifecycleRequireDocker(t *testing.T) {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}

	cli, err := dockerclient.New(dockerclient.FromEnv)
	if err != nil {
		dockerProxyLifecycleSkipOrFailf(t, "docker daemon unavailable: %v", err)
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx, dockerclient.PingOptions{}); err != nil {
		dockerProxyLifecycleSkipOrFailf(t, "docker daemon unreachable: %v", err)
	}
}

func dockerProxyLifecycleSkipOrFailf(t *testing.T, format string, args ...any) {
	t.Helper()

	strict := os.Getenv("TESTKIT_STRICT") == "1" ||
		(os.Getenv("CI") != "" && os.Getenv("TESTKIT_STRICT") != "0")
	if strict {
		t.Fatalf(format, args...)
	}
	t.Skipf(format, args...)
}

func dockerProxyLifecycleRepoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
}

// dockerProxyLifecycleRouteConfig rewrites only the two backend socket
// endpoints so tests can observe them. Every frontend, ACL, routing and retry
// rule remains the deployed text.
func dockerProxyLifecycleRouteConfig(t *testing.T, raw, defaultAddress, mutationAddress string) string {
	t.Helper()

	const socketServer = "server dockersocket /var/run/docker.sock"
	parts := strings.Split(raw, socketServer)
	require.Len(t, parts, 3, "expected dockerbackend and dockermutations socket servers")

	return parts[0] + "server dockersocket " + defaultAddress +
		parts[1] + "server dockersocket " + mutationAddress + parts[2]
}

type dockerProxyLifecycleDaemon struct {
	t        *testing.T
	server   *httptest.Server
	mu       sync.Mutex
	total    int
	dropNext atomic.Bool
}

func newDockerProxyLifecycleDaemon(t *testing.T) *dockerProxyLifecycleDaemon {
	t.Helper()

	//nolint:gosec // The fake Docker daemon must be reachable from the proxy container.
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	require.NoError(t, err)

	daemon := &dockerProxyLifecycleDaemon{t: t}
	daemon.server = httptest.NewUnstartedServer(http.HandlerFunc(daemon.serveHTTP))
	daemon.server.Listener = listener
	daemon.server.Start()
	t.Cleanup(daemon.server.Close)
	return daemon
}

func (d *dockerProxyLifecycleDaemon) serveHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	d.total++
	d.mu.Unlock()

	if d.dropNext.CompareAndSwap(true, false) {
		// Deliberately abandon the request body; the connection aborts before a reply.
		_, _ = io.Copy(io.Discard, r.Body) //nolint:errcheck // Intentional disconnect; body errors cannot be reported.
		// Accept the mutation, then lose the reply without a response.
		panic(http.ErrAbortHandler)
	}

	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.HasSuffix(r.URL.Path, "/containers/create"):
		w.WriteHeader(http.StatusCreated)
		writeDockerTestResponse(d.t, w, `{"Id":"`+strings.Repeat("a", 64)+`","Warnings":[]}`)
	case strings.HasSuffix(r.URL.Path, "/start"),
		strings.HasSuffix(r.URL.Path, "/stop"),
		r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	default:
		writeDockerTestResponse(d.t, w, `{"ID":"fake-daemon","Version":"28.5.2"}`)
	}
}

func (d *dockerProxyLifecycleDaemon) port(t *testing.T) string {
	t.Helper()

	address, ok := d.server.Listener.Addr().(*net.TCPAddr)
	require.True(t, ok)
	return strconv.Itoa(address.Port)
}

func (d *dockerProxyLifecycleDaemon) reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.total = 0
}

func (d *dockerProxyLifecycleDaemon) totalRequests() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.total
}

type dockerProxyLifecycleProxy struct {
	port string
}

func startDockerProxyLifecycleProxy(
	t *testing.T,
	ca *dockerProxyTestCA,
	config string,
	defaultAddress, mutationAddress string,
	waitStrategy wait.Strategy,
	command string,
) testcontainers.Container {
	t.Helper()

	directory := t.TempDir()
	configFile := filepath.Join(directory, "haproxy.cfg")
	caFile := filepath.Join(directory, "ca.crt")
	serverFile := filepath.Join(directory, "server.pem")
	server := issueDockerProxyTestCertificate(t, ca, 9002, "docker-proxy", []string{"docker-proxy"}, x509.ExtKeyUsageServerAuth)
	writeDockerProxyTestFile(t, configFile, []byte(dockerProxyLifecycleRouteConfig(t, config, defaultAddress, mutationAddress)))
	writeDockerProxyTestFile(t, caFile, ca.pem)
	writeDockerProxyTestFile(t, serverFile, []byte(string(server.certPEM)+string(server.keyPEM)))

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	t.Cleanup(cancel)

	container, err := testcontainers.Run(
		ctx,
		dockerProxyLifecycleImage,
		testcontainers.WithFiles(
			testcontainers.ContainerFile{HostFilePath: configFile, ContainerFilePath: "/etc/haproxy/haproxy.cfg", FileMode: 0o644},
			testcontainers.ContainerFile{HostFilePath: caFile, ContainerFilePath: "/certs/docker-proxy/ca.crt", FileMode: 0o644},
			testcontainers.ContainerFile{HostFilePath: serverFile, ContainerFilePath: "/certs/docker-proxy/server.pem", FileMode: 0o644},
		),
		testcontainers.WithEnv(map[string]string{"DOCKER_PROXY_TOKEN": dockerProxyLifecycleToken}),
		testcontainers.WithEntrypoint("/bin/sh", "-ec"),
		testcontainers.WithCmd(command),
		testcontainers.WithExposedPorts(dockerProxyLifecyclePort),
		testcontainers.WithHostConfigModifier(func(hostConfig *dockertypes.HostConfig) {
			hostConfig.ExtraHosts = append(hostConfig.ExtraHosts, dockerProxyLifecycleHost+":host-gateway")
		}),
		testcontainers.WithWaitStrategy(waitStrategy),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := container.Terminate(cleanupCtx); err != nil {
			t.Errorf("terminate docker proxy container: %v", err)
		}
	})
	return container
}

func dockerProxyLifecycleClient(t *testing.T, ca *dockerProxyTestCA, identity *dockerProxyTestCertificate) *http.Client {
	t.Helper()

	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(ca.pem))
	tlsConfig := &tls.Config{
		RootCAs:    roots,
		ServerName: "docker-proxy",
		MinVersion: tls.VersionTLS12,
	}
	if identity != nil {
		pair, err := tls.X509KeyPair(identity.certPEM, identity.keyPEM)
		require.NoError(t, err)
		tlsConfig.Certificates = []tls.Certificate{pair}
	}

	transport := &http.Transport{
		TLSClientConfig:   tlsConfig,
		DisableKeepAlives: true,
	}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func dockerProxyLifecycleRequest(
	ctx context.Context,
	client *http.Client,
	port, method, path, token string,
) (int, error) {
	var body io.Reader
	if method == http.MethodPost {
		body = strings.NewReader("{}")
	}
	request, err := http.NewRequestWithContext(ctx, method, "https://127.0.0.1:"+port+path, body)
	if err != nil {
		return 0, err
	}
	if token != "" {
		request.Header.Set("X-Chronoverse-Docker-Proxy-Token", token)
	}

	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = response.Body.Close()
	}()
	if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, 4096)); err != nil {
		return 0, err
	}
	return response.StatusCode, nil
}

func dockerProxyLifecycleAssertNoMutationReplay(
	t *testing.T,
	client *http.Client,
	proxy *dockerProxyLifecycleProxy,
	defaultDaemon, mutationDaemon *dockerProxyLifecycleDaemon,
	path string,
	successStatus int,
) {
	t.Helper()

	defaultDaemon.reset()
	mutationDaemon.reset()
	mutationDaemon.dropNext.Store(true)

	status, err := dockerProxyLifecycleRequest(t.Context(), client, proxy.port, http.MethodPost, path, dockerProxyLifecycleToken)
	if err == nil {
		require.NotEqual(t, successStatus, status, "accepted mutation was falsely reported as success")
	}
	require.Equal(t, 1, mutationDaemon.totalRequests(), "mutation was transmitted more than once")
	time.Sleep(250 * time.Millisecond)
	require.Equal(t, 1, mutationDaemon.totalRequests(), "mutation replay happened after the lost reply")
	require.Zero(t, defaultDaemon.totalRequests(), "mutation used the retrying backend")

	status, err = dockerProxyLifecycleRequest(t.Context(), client, proxy.port, http.MethodPost, path, dockerProxyLifecycleToken)
	require.NoError(t, err)
	require.Equal(t, successStatus, status)
	require.Equal(t, 2, mutationDaemon.totalRequests(), "a fresh mutation must reach the daemon once")
}

func dockerProxyLifecycleAssertBackend(t *testing.T, defaultDaemon, mutationDaemon *dockerProxyLifecycleDaemon, backend string) {
	t.Helper()

	wantDefault, wantMutation := 0, 0
	switch backend {
	case "default":
		wantDefault = 1
	case "mutation":
		wantMutation = 1
	}
	require.Equal(t, wantDefault, defaultDaemon.totalRequests(), "dockerbackend transmissions")
	require.Equal(t, wantMutation, mutationDaemon.totalRequests(), "dockermutations transmissions")
}

func dockerProxyLifecycleRunCases(
	t *testing.T,
	proxy *dockerProxyLifecycleProxy,
	client *http.Client,
	defaultDaemon, mutationDaemon *dockerProxyLifecycleDaemon,
	cases []dockerProxyLifecycleCase,
) {
	t.Helper()

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			defaultDaemon.reset()
			mutationDaemon.reset()

			status, err := dockerProxyLifecycleRequest(t.Context(), client, proxy.port, testCase.method, testCase.path, dockerProxyLifecycleToken)
			require.NoError(t, err)
			require.Equal(t, testCase.status, status)
			dockerProxyLifecycleAssertBackend(t, defaultDaemon, mutationDaemon, testCase.backend)
		})
	}
}

func TestIntegrationDockerProxyLifecycleConfigParser(t *testing.T) {
	dockerProxyLifecycleRequireDocker(t)

	ca := newDockerProxyTestCA(t, 9001, "docker-proxy-lifecycle-ca")
	for _, source := range dockerProxyLifecycleConfigs(t) {
		t.Run(source.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()

			container := startDockerProxyLifecycleProxy(t, ca, source.content, "127.0.0.1:1", "127.0.0.1:2",
				wait.ForExit().WithExitTimeout(60*time.Second),
				"exec haproxy -c -f /etc/haproxy/haproxy.cfg")
			state, err := container.State(ctx)
			require.NoError(t, err)
			require.Equal(t, 0, state.ExitCode, "pinned HAProxy rejected %s config", source.name)
		})
	}
}

func TestIntegrationDockerProxyLifecycleContracts(t *testing.T) {
	dockerProxyLifecycleRequireDocker(t)

	for _, source := range dockerProxyLifecycleConfigs(t) {
		t.Run(source.name, func(t *testing.T) {
			defaultDaemon := newDockerProxyLifecycleDaemon(t)
			mutationDaemon := newDockerProxyLifecycleDaemon(t)
			ca := newDockerProxyTestCA(t, 9001, "docker-proxy-lifecycle-ca")

			container := startDockerProxyLifecycleProxy(t, ca, source.content,
				net.JoinHostPort(dockerProxyLifecycleHost, defaultDaemon.port(t)),
				net.JoinHostPort(dockerProxyLifecycleHost, mutationDaemon.port(t)),
				wait.ForListeningPort(dockerProxyLifecyclePort).WithStartupTimeout(60*time.Second),
				"exec haproxy -W -db -f /etc/haproxy/haproxy.cfg")
			mappedPort, err := container.MappedPort(t.Context(), dockerProxyLifecyclePort)
			require.NoError(t, err)
			proxy := &dockerProxyLifecycleProxy{port: mappedPort.Port()}

			runtimeAgent := issueDockerProxyTestCertificate(t, ca, 9011, "docker-proxy-client-runtime-agent", nil, x509.ExtKeyUsageClientAuth)
			workflow := issueDockerProxyTestCertificate(t, ca, 9012, "docker-proxy-client-workflow-worker", nil, x509.ExtKeyUsageClientAuth)
			execution := issueDockerProxyTestCertificate(t, ca, 9013, "docker-proxy-client-execution-worker", nil, x509.ExtKeyUsageClientAuth)
			unlisted := issueDockerProxyTestCertificate(t, ca, 9014, "docker-proxy-client-unlisted", nil, x509.ExtKeyUsageClientAuth)
			serverName := issueDockerProxyTestCertificate(t, ca, 9015, "docker-proxy", nil, x509.ExtKeyUsageClientAuth)

			runtimeCases := []dockerProxyLifecycleCase{
				{name: "ping", method: http.MethodGet, path: "/_ping", status: http.StatusOK, backend: "default"},
				{name: "version", method: http.MethodGet, path: "/version", status: http.StatusOK, backend: "default"},
				{name: "versioned version", method: http.MethodGet, path: "/v1.51/version", status: http.StatusOK, backend: "default"},
				{name: "head ping", method: http.MethodHead, path: "/_ping", status: http.StatusOK, backend: "default"},
				{name: "info denied", method: http.MethodGet, path: "/info", status: http.StatusForbidden},
				{name: "versioned info denied", method: http.MethodGet, path: "/v1.51/info", status: http.StatusForbidden},
				{name: "create denied", method: http.MethodPost, path: "/v1.51/containers/create", status: http.StatusForbidden},
				{name: "inspect denied", method: http.MethodGet, path: "/v1.51/containers/probe/json", status: http.StatusForbidden},
				{name: "image inspect denied", method: http.MethodGet, path: "/v1.51/images/alpine/json", status: http.StatusForbidden},
				{name: "delete denied", method: http.MethodDelete, path: "/v1.51/containers/probe", status: http.StatusForbidden},
			}
			workflowCases := []dockerProxyLifecycleCase{
				{name: "ping", method: http.MethodGet, path: "/_ping", status: http.StatusOK, backend: "default"},
				{name: "version", method: http.MethodGet, path: "/version", status: http.StatusOK, backend: "default"},
				{name: "info", method: http.MethodGet, path: "/info", status: http.StatusOK, backend: "default"},
				{name: "versioned info", method: http.MethodGet, path: "/v1.51/info", status: http.StatusOK, backend: "default"},
				{name: "head info", method: http.MethodHead, path: "/info", status: http.StatusOK, backend: "default"},
				{name: "image inspect", method: http.MethodGet, path: "/v1.51/images/alpine/json", status: http.StatusOK, backend: "default"},
				{name: "image create", method: http.MethodPost, path: "/v1.51/images/create?fromImage=alpine", status: http.StatusOK, backend: "default"},
				{name: "logs", method: http.MethodGet, path: "/v1.51/containers/probe/logs", status: http.StatusOK, backend: "default"},
				{name: "stop", method: http.MethodPost, path: "/v1.51/containers/probe/stop", status: http.StatusNoContent, backend: "default"},
				{name: "delete", method: http.MethodDelete, path: "/v1.51/containers/probe", status: http.StatusNoContent, backend: "default"},
				{name: "inspect denied", method: http.MethodGet, path: "/v1.51/containers/probe/json", status: http.StatusForbidden},
				{name: "list denied", method: http.MethodGet, path: "/v1.51/containers/json", status: http.StatusForbidden},
				{name: "start denied", method: http.MethodPost, path: "/v1.51/containers/probe/start", status: http.StatusForbidden},
				{name: "create denied", method: http.MethodPost, path: "/v1.51/containers/create?name=probe", status: http.StatusForbidden},
				{name: "network inspect denied", method: http.MethodGet, path: "/v1.51/networks/probe", status: http.StatusForbidden},
				{name: "network create denied", method: http.MethodPost, path: "/v1.51/networks/create", status: http.StatusForbidden},
				{name: "post info denied", method: http.MethodPost, path: "/v1.51/info", status: http.StatusForbidden},
				{name: "delete image denied", method: http.MethodDelete, path: "/v1.51/images/alpine", status: http.StatusForbidden},
			}
			executionCases := []dockerProxyLifecycleCase{
				{name: "ping", method: http.MethodGet, path: "/_ping", status: http.StatusOK, backend: "default"},
				{name: "version", method: http.MethodGet, path: "/version", status: http.StatusOK, backend: "default"},
				{name: "info", method: http.MethodGet, path: "/info", status: http.StatusOK, backend: "default"},
				{name: "versioned info", method: http.MethodGet, path: "/v1.51/info", status: http.StatusOK, backend: "default"},
				{name: "image inspect", method: http.MethodGet, path: "/v1.51/images/alpine/json", status: http.StatusOK, backend: "default"},
				{name: "image create", method: http.MethodPost, path: "/v1.51/images/create?fromImage=alpine", status: http.StatusOK, backend: "default"},
				{name: "inspect", method: http.MethodGet, path: "/v1.51/containers/probe/json", status: http.StatusOK, backend: "default"},
				{name: "logs", method: http.MethodGet, path: "/v1.51/containers/probe/logs", status: http.StatusOK, backend: "default"},
				{name: "create", method: http.MethodPost, path: "/v1.51/containers/create?name=probe", status: http.StatusCreated, backend: "mutation"},
				{name: "start", method: http.MethodPost, path: "/v1.51/containers/probe/start", status: http.StatusNoContent, backend: "mutation"},
				{name: "stop", method: http.MethodPost, path: "/v1.51/containers/probe/stop", status: http.StatusNoContent, backend: "default"},
				{name: "wait", method: http.MethodPost, path: "/v1.51/containers/probe/wait", status: http.StatusOK, backend: "default"},
				{name: "delete", method: http.MethodDelete, path: "/v1.51/containers/probe", status: http.StatusNoContent, backend: "default"},
				{name: "network inspect", method: http.MethodGet, path: "/v1.51/networks/probe", status: http.StatusOK, backend: "default"},
				{name: "network create", method: http.MethodPost, path: "/v1.51/networks/create", status: http.StatusOK, backend: "default"},
				{name: "list denied", method: http.MethodGet, path: "/v1.51/containers/json", status: http.StatusForbidden},
				{name: "exec denied", method: http.MethodPost, path: "/v1.51/containers/probe/exec", status: http.StatusForbidden},
				{name: "connect denied", method: http.MethodPost, path: "/v1.51/networks/probe/connect", status: http.StatusForbidden},
				{name: "delete image denied", method: http.MethodDelete, path: "/v1.51/images/alpine", status: http.StatusForbidden},
				{name: "post info denied", method: http.MethodPost, path: "/v1.51/info", status: http.StatusForbidden},
				{name: "volumes denied", method: http.MethodGet, path: "/v1.51/volumes", status: http.StatusForbidden},
			}
			unlistedCases := []dockerProxyLifecycleCase{
				{name: "ping denied", method: http.MethodGet, path: "/_ping", status: http.StatusForbidden},
				{name: "info denied", method: http.MethodGet, path: "/info", status: http.StatusForbidden},
				{name: "inspect denied", method: http.MethodGet, path: "/v1.51/containers/probe/json", status: http.StatusForbidden},
				{name: "create denied", method: http.MethodPost, path: "/v1.51/containers/create", status: http.StatusForbidden},
				{name: "delete denied", method: http.MethodDelete, path: "/v1.51/containers/probe", status: http.StatusForbidden},
			}

			t.Run("runtime-agent identity and cleanup role denial", func(t *testing.T) {
				dockerProxyLifecycleRunCases(t, proxy, dockerProxyLifecycleClient(t, ca, &runtimeAgent),
					defaultDaemon, mutationDaemon, runtimeCases)
			})
			t.Run("workflow certificate role boundaries", func(t *testing.T) {
				dockerProxyLifecycleRunCases(t, proxy, dockerProxyLifecycleClient(t, ca, &workflow),
					defaultDaemon, mutationDaemon, workflowCases)
			})
			t.Run("execution certificate role boundaries", func(t *testing.T) {
				dockerProxyLifecycleRunCases(t, proxy, dockerProxyLifecycleClient(t, ca, &execution),
					defaultDaemon, mutationDaemon, executionCases)
			})
			t.Run("unlisted trusted certificate is denied", func(t *testing.T) {
				dockerProxyLifecycleRunCases(t, proxy, dockerProxyLifecycleClient(t, ca, &unlisted),
					defaultDaemon, mutationDaemon, unlistedCases)
			})
			t.Run("server certificate cannot act as a worker", func(t *testing.T) {
				dockerProxyLifecycleRunCases(t, proxy, dockerProxyLifecycleClient(t, ca, &serverName),
					defaultDaemon, mutationDaemon, unlistedCases)
			})

			t.Run("token is required before authorization", func(t *testing.T) {
				client := dockerProxyLifecycleClient(t, ca, &workflow)
				for _, token := range []string{"", "wrong-token"} {
					defaultDaemon.reset()
					mutationDaemon.reset()
					status, err := dockerProxyLifecycleRequest(t.Context(), client, proxy.port, http.MethodGet, "/info", token)
					require.NoError(t, err)
					require.Equal(t, http.StatusForbidden, status)
					dockerProxyLifecycleAssertBackend(t, defaultDaemon, mutationDaemon, "")
				}
			})

			t.Run("untrusted or absent certificate is a handshake failure", func(t *testing.T) {
				defaultDaemon.reset()
				mutationDaemon.reset()

				withoutCertificate := dockerProxyLifecycleClient(t, ca, nil)
				_, err := dockerProxyLifecycleRequest(t.Context(), withoutCertificate, proxy.port, http.MethodGet, "/_ping", dockerProxyLifecycleToken)
				require.Error(t, err)

				foreignCA := newDockerProxyTestCA(t, 9003, "docker-proxy-lifecycle-foreign-ca")
				foreign := issueDockerProxyTestCertificate(t, foreignCA, 9016, "docker-proxy-client-workflow-worker", nil, x509.ExtKeyUsageClientAuth)
				withForeignCertificate := dockerProxyLifecycleClient(t, ca, &foreign)
				_, err = dockerProxyLifecycleRequest(t.Context(), withForeignCertificate, proxy.port, http.MethodGet, "/v1.51/info", dockerProxyLifecycleToken)
				require.Error(t, err)
				dockerProxyLifecycleAssertBackend(t, defaultDaemon, mutationDaemon, "")
			})

			t.Run("lost create reply is not replayed", func(t *testing.T) {
				client := dockerProxyLifecycleClient(t, ca, &execution)
				dockerProxyLifecycleAssertNoMutationReplay(t, client, proxy, defaultDaemon, mutationDaemon,
					"/v1.51/containers/create?name=replay-probe", http.StatusCreated)
			})

			t.Run("lost start reply is not replayed", func(t *testing.T) {
				client := dockerProxyLifecycleClient(t, ca, &execution)
				dockerProxyLifecycleAssertNoMutationReplay(t, client, proxy, defaultDaemon, mutationDaemon,
					"/v1.51/containers/replay-probe/start", http.StatusNoContent)
			})
		})
	}
}

func TestIntegrationDockerProxyReplayProbeDetectsUnsafePolicy(t *testing.T) {
	dockerProxyLifecycleRequireDocker(t)
	for _, source := range dockerProxyLifecycleConfigs(t) {
		t.Run(source.name, func(t *testing.T) {
			daemon := newDockerProxyLifecycleDaemon(t)
			ca := newDockerProxyTestCA(t, 9101, "docker-proxy-negative-control")
			// Change policy only: the same accepted-mutation fault must expose a replay.
			unsafe := strings.Replace(source.content, "retries 0", "retries 2\n  retry-on all-retryable-errors\n  option http-buffer-request", 1)
			unsafe = strings.Replace(unsafe, "no option redispatch", "option redispatch", 1)
			address := net.JoinHostPort(dockerProxyLifecycleHost, daemon.port(t))
			proxy := startDockerProxyLifecycleProxy(t, ca, unsafe, address, address,
				wait.ForListeningPort(dockerProxyLifecyclePort).WithStartupTimeout(60*time.Second),
				"exec haproxy -W -db -f /etc/haproxy/haproxy.cfg")
			port, err := proxy.MappedPort(t.Context(), dockerProxyLifecyclePort)
			require.NoError(t, err)
			identity := issueDockerProxyTestCertificate(t, ca, 9102, "docker-proxy-client-execution-worker", nil, x509.ExtKeyUsageClientAuth)
			client := dockerProxyLifecycleClient(t, ca, &identity)
			for _, path := range []string{"/v1.51/containers/create", "/v1.51/containers/" + strings.Repeat("a", 64) + "/start"} {
				daemon.reset()
				daemon.dropNext.Store(true)
				_, err = dockerProxyLifecycleRequest(t.Context(), client, port.Port(), http.MethodPost, path, dockerProxyLifecycleToken)
				require.NoError(t, err)
				require.Greater(t, daemon.totalRequests(), 1, "fault probe failed to detect deliberately unsafe mutation replay")
			}
		})
	}
}
