// Native startup tests exercise the shipped commands in real processes.
package main_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

const module = "github.com/hitesh22rana/chronoverse"

func TestMain(m *testing.M) { testkit.Run(m, testkit.WithPostgres(), testkit.WithRedis()) }

// Each build is removed before the next one, keeping the integration suite's
// disk use bounded. Instrument main and its application dependencies, including os.Exit.
func build(t *testing.T, name string) (string, string) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), name)
	svc := module + "/internal/pkg/svc."
	ld := "-X " + svc + "name=" + name + " -X " + svc + "authPrivateKeyPath=auth.ed -X " + svc + "authPublicKeyPath=auth.ed.pub"
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-race", "-cover", "-covermode=atomic", "-coverpkg="+module+"/cmd/...,"+module+"/internal/...,"+module+"/pkg/...", "-ldflags", ld, "-o", bin, "./cmd/"+name)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", name, err, out)
	}
	cover := t.TempDir()
	if base := os.Getenv("CHRONOVERSE_COMMAND_COVERAGE_DIR"); base != "" {
		cover = filepath.Join(base, name)
		if err := os.MkdirAll(cover, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return bin, cover
}

func environment(cover string, changes ...string) []string {
	values := map[string]string{
		"ENV": "development", "CRYPTO_SECRET": strings.Repeat("a", 32), "SERVER_CSRF_HMAC_SECRET": strings.Repeat("b", 32),
		"GRPC_PORT": "8080", "KAFKA_BROKERS": "localhost:9092", "KAFKA_CONSUMER_GROUP": "startup-test", "SERVER_HOST": "127.0.0.1", "MEILISEARCH_MASTER_KEY": "startup-test",
		"USERS_SERVICE_HOST": "localhost", "USERS_SERVICE_PORT": "8081", "WORKFLOWS_SERVICE_HOST": "localhost", "WORKFLOWS_SERVICE_PORT": "8082",
		"JOBS_SERVICE_HOST": "localhost", "JOBS_SERVICE_PORT": "8083", "NOTIFICATIONS_SERVICE_HOST": "localhost", "NOTIFICATIONS_SERVICE_PORT": "8084",
		"ANALYTICS_SERVICE_HOST": "localhost", "ANALYTICS_SERVICE_PORT": "8085", "GOCOVERDIR": cover,
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:1", "OTEL_EXPORTER_OTLP_TIMEOUT": "1", "OTEL_METRIC_EXPORT_INTERVAL": "3600000",
		"RUNTIME_AGENT_ID": "startup-test", "RUNTIME_AGENT_NODE_NAME": "startup-test", "RUNTIME_AGENT_DOCKER_ENDPOINT": "tcp://127.0.0.1:2376",
	}
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "TMP", "TEMP", "GORACE", "GODEBUG"} {
		if v, ok := os.LookupEnv(key); ok {
			values[key] = v
		}
	}
	for _, entry := range changes {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
	}
	result := make([]string, 0, len(values))
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}

func keys(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, path), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("auth.ed", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}))
	write("auth.ed.pub", pubPEM)
	if err := os.MkdirAll(filepath.Join(dir, "certs/issuers"), 0o700); err != nil {
		t.Fatal(err)
	}
	bundle, err := json.Marshal(map[string]map[string]string{"startup-key": {"iss": name, "pub": string(pubPEM)}})
	if err != nil {
		t.Fatal(err)
	}
	write("certs/issuers/trusted.json", bundle)
	return dir
}

func reject(t *testing.T, bin, dir, cover, want string, changes ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	//nolint:gosec // Runs a binary built from this checkout in an isolated fixture directory.
	cmd := exec.CommandContext(ctx, bin)
	cmd.Dir = dir
	cmd.Env = environment(cover, changes...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("startup did not terminate: %v\n%s", ctx.Err(), out)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("exit = %v, want status 1\n%s", err, out)
	}
	if !strings.Contains(string(out), want) {
		t.Fatalf("missing original dependency error %q:\n%s", want, out)
	}
}

// Supported configuration errors must make it through main as a real nonzero
// OS exit. Valid signing fixtures then reach actual dependency constructors.
func TestIntegrationNativeStartupFailures(t *testing.T) {
	if testing.Short() {
		t.Skip("native binary startup integration")
	}
	names := []string{
		"analytics-processor", "analytics-service", "database-migration", "execution-worker", "joblogs-processor",
		"jobs-service", "notifications-service", "outbox-relay", "runtime-agent", "scheduling-worker",
		"server", "users-service", "workflow-worker", "workflows-service",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			bin, cover := build(t, name)
			dir := keys(t, name)
			configKey := "POSTGRES_PORT"
			if name == "execution-worker" || name == "workflow-worker" || name == "server" {
				configKey = "REDIS_PORT"
			}
			reject(t, bin, dir, cover, configKey, configKey+"=invalid")
			switch name {
			case "database-migration":
				for _, tls := range []bool{false, true} {
					reject(t, bin, dir, cover, "failed to load client key pair",
						"CLICKHOUSE_TLS_ENABLED=true", "CLICKHOUSE_TLS_CERT_FILE=missing-client.pem", "CLICKHOUSE_TLS_KEY_FILE=missing-client.key",
						fmt.Sprintf("POSTGRES_TLS_ENABLED=%t", tls), "POSTGRES_TLS_CA_FILE=ca path.pem",
						"POSTGRES_TLS_CERT_FILE=cert path.pem", "POSTGRES_TLS_KEY_FILE=key path.pem")
				}
			case "joblogs-processor", "workflow-worker":
				reject(t, bin, dir, cover, "missing-client.pem", "REDIS_TLS_ENABLED=true", "REDIS_TLS_CERT_FILE=missing-client.pem", "REDIS_TLS_KEY_FILE=missing-client.key")
			case "server":
				for _, service := range []string{"USERS", "WORKFLOWS", "JOBS", "NOTIFICATIONS", "ANALYTICS"} {
					reject(t, bin, dir, cover, "missing-ca.pem", service+"_SERVICE_TLS_ENABLED=true", service+"_SERVICE_TLS_CA_FILE=missing-ca.pem")
				}
			case "execution-worker":
				reject(t, bin, dir, cover, "missing-client.pem", "KAFKA_TLS_ENABLED=true", "KAFKA_TLS_CERT_FILE=missing-client.pem", "KAFKA_TLS_KEY_FILE=missing-client.key")
			default:
				reject(t, bin, dir, cover, "missing-ca.pem", "POSTGRES_TLS_ENABLED=true", "POSTGRES_TLS_CA_FILE=missing-ca.pem")
			}
			if commandUsesAuth(name) {
				reject(t, bin, t.TempDir(), cover, "failed to read private key")
			}
			assertCoverage(t, cover, name)
		})
	}
}

func commandUsesAuth(name string) bool {
	switch name {
	case "analytics-service", "execution-worker", "jobs-service", "notifications-service", "server", "users-service", "workflow-worker", "workflows-service":
		return true
	default:
		return false
	}
}

// Native os.Exit writes covdata independently of the go test driver. Verify the
// artifacts explicitly, so a green subprocess test cannot silently lose them.
func assertCoverage(t *testing.T, cover, name string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "go", "tool", "covdata", "func", "-i="+cover)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("covdata: %v\n%s", err, out)
	}
	mainFound, runFound := false, false
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		switch fields[len(fields)-2] {
		case "main":
			mainFound = fields[len(fields)-1] == "100.0%"
		case "run":
			runFound = fields[len(fields)-1] != "0.0%"
		}
	}
	if !mainFound || !runFound {
		t.Fatalf("native %s main/run missing execution counters:\n%s", name, out)
	}
	profile := filepath.Join(cover, "coverage.out")
	cmd = exec.CommandContext(t.Context(), "go", "tool", "covdata", "textfmt", "-i="+cover, "-o="+profile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("convert native profile: %v\n%s", err, out)
	}
	t.Logf("native command coverage: %s\n%s", profile, out)
}

type outputBuffer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (b *outputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(p)
}
func (b *outputBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.data.String() }

func postgresEnv(t *testing.T) []string {
	t.Helper()
	u, err := url.Parse(testkit.PostgresDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	password, _ := u.User.Password()
	return []string{"POSTGRES_HOST=" + u.Hostname(), "POSTGRES_PORT=" + u.Port(), "POSTGRES_USER=" + u.User.Username(), "POSTGRES_PASSWORD=" + password, "POSTGRES_DB=" + strings.TrimPrefix(u.Path, "/")}
}

func TestIntegrationNativePostgresLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("real PostgreSQL integration")
	}
	pg := postgresEnv(t)
	for _, name := range []string{"analytics-service", "notifications-service", "scheduling-worker", "users-service", "workflows-service", "server"} {
		t.Run(name, func(t *testing.T) {
			bin, cover := build(t, name)
			dir := keys(t, name)
			dependencies := append([]string{}, pg...)
			if name == "users-service" || name == "workflows-service" || name == "server" {
				dependencies = append(dependencies, redisEnv(t)...)
			}
			port := availableCommandPort(t, name, bin, dir, cover, dependencies)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			//nolint:gosec // Runs a binary built from this checkout in an isolated fixture directory.
			cmd := exec.CommandContext(ctx, bin)
			cmd.Dir = dir
			changes := append(append([]string{}, dependencies...), commandPortSetting(name)+"="+strconv.Itoa(port), "SCHEDULING_WORKER_POLL_INTERVAL=50ms")
			cmd.Env = environment(cover, changes...)
			var output outputBuffer
			cmd.Stdout = &output
			cmd.Stderr = &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			defer func() {
				if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
					t.Errorf("stop native command during cleanup: %v", err)
				}
			}()
			waitNativeReady(t, name, port, ctx, done, &output)
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("graceful shutdown: %v\n%s", err, output.String())
				}
			case <-ctx.Done():
				t.Fatalf("shutdown timed out\n%s", output.String())
			}
			assertCoverage(t, cover, name)
		})
	}
}

// Holding the socket while the native process starts verifies a real bind
// failure. Closing it then allows the same command to prove successful startup.
func availableCommandPort(t *testing.T, name, bin, dir, cover string, pg []string) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close reserved socket: %v", err)
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	if name != "scheduling-worker" {
		message := "failed to create listener"
		if name == "server" {
			message = "address already in use"
		}
		reject(t, bin, dir, cover, message, append(append([]string{}, pg...), commandPortSetting(name)+"="+strconv.Itoa(port))...)
	}
	return port
}

// A valid database must be acquired before the subsequent Redis certificate
// error. This exercises partial startup and the production database defer.
func TestIntegrationNativePartialAcquisitionFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("real PostgreSQL integration")
	}
	pg := postgresEnv(t)
	for _, name := range []string{"users-service", "workflows-service", "jobs-service"} {
		t.Run(name, func(t *testing.T) {
			bin, cover := build(t, name)
			changes := append(append([]string{}, pg...), "REDIS_TLS_ENABLED=true", "REDIS_TLS_CERT_FILE=missing-client.pem", "REDIS_TLS_KEY_FILE=missing-client.key")
			reject(t, bin, keys(t, name), cover, "missing-client.pem", changes...)
			assertCoverage(t, cover, name)
		})
	}
}

func waitReady(t *testing.T, ctx context.Context, done <-chan error, out *outputBuffer, ready func() bool) {
	t.Helper()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ready() {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("command exited before readiness: %v\n%s", err, out.String())
		case <-ctx.Done():
			t.Fatalf("readiness timed out\n%s", out.String())
		case <-ticker.C:
		}
	}
}

func commandPortSetting(name string) string {
	if name == "server" {
		return "SERVER_PORT"
	}
	return "GRPC_PORT"
}

func redisEnv(t *testing.T) []string {
	t.Helper()
	host, port, err := net.SplitHostPort(testkit.RedisAddr(t))
	if err != nil {
		t.Fatal(err)
	}
	return []string{"REDIS_HOST=" + host, "REDIS_PORT=" + port}
}

func waitNativeReady(t *testing.T, name string, port int, ctx context.Context, done <-chan error, output *outputBuffer) {
	t.Helper()
	if name == "scheduling-worker" {
		waitReady(t, ctx, done, output, func() bool { return strings.Contains(output.String(), "successfully scheduled jobs") })
		return
	}
	if name == "server" {
		client := &http.Client{Timeout: 200 * time.Millisecond}
		t.Cleanup(client.CloseIdleConnections)
		waitReady(t, ctx, done, output, func() bool {
			response, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/auth/login")
			if err != nil {
				return false
			}
			_, readErr := io.Copy(io.Discard, response.Body)
			closeErr := response.Body.Close()
			return readErr == nil && closeErr == nil && response.StatusCode == http.StatusMethodNotAllowed && response.Header.Get("Allow") == http.MethodPost
		})
		return
	}
	conn, err := grpc.NewClient("127.0.0.1:"+strconv.Itoa(port), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close native health client: %v", err)
		}
	})
	client := grpc_health_v1.NewHealthClient(conn)
	waitReady(t, ctx, done, output, func() bool {
		callCtx, stop := context.WithTimeout(ctx, 200*time.Millisecond)
		defer stop()
		response, err := client.Check(callCtx, &grpc_health_v1.HealthCheckRequest{Service: name})
		return err == nil && response.Status == grpc_health_v1.HealthCheckResponse_SERVING
	})
}

func TestIntegrationNativeExecutorPartialStartup(t *testing.T) {
	if testing.Short() {
		t.Skip("real Redis integration")
	}
	bin, cover := build(t, "execution-worker")
	dir := keys(t, "execution-worker")
	redis := redisEnv(t)
	for _, service := range []string{"WORKFLOWS", "JOBS"} {
		changes := append(append([]string{}, redis...), service+"_SERVICE_TLS_ENABLED=true", service+"_SERVICE_TLS_CA_FILE=missing-ca.pem")
		reject(t, bin, dir, cover, "missing-ca.pem", changes...)
	}
	changes := append(append([]string{}, redis...), "EXECUTION_WORKER_CONCURRENCY=2", "EXECUTION_WORKER_AWAITING_RECONCILIATION_LIMIT=1")
	reject(t, bin, dir, cover, "awaiting reconciliation limit", changes...)
	assertCoverage(t, cover, "execution-worker")
}
