//nolint:testpackage // Subprocesses exercise Fatal/Skip without weakening test reporting.
package testkit

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"github.com/meilisearch/meilisearch-go"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestStrictModeHonorsExplicitOverrides(t *testing.T) {
	for _, tc := range []struct {
		strict, ci string
		want       bool
	}{
		{"", "", false},
		{"", "true", true},
		{"", "false", false},
		{"1", "false", true},
		{"0", "true", false},
		{"invalid", "true", false},
	} {
		t.Run(tc.strict+"/"+tc.ci, func(t *testing.T) {
			t.Setenv("TESTKIT_STRICT", tc.strict)
			t.Setenv("CI", tc.ci)
			require.Equal(t, tc.want, strictMode())
		})
	}
	t.Setenv("CI", "true")
	t.Setenv("TESTKIT_STRICT", "")
	require.NoError(t, os.Unsetenv("TESTKIT_STRICT"))
	require.True(t, strictMode())
}

type closingSearchFixture struct {
	meilisearch.ServiceManager
	calls int
}

func (f *closingSearchFixture) Close() { f.calls++ }

func TestRunSuitePreservesExitStatusAndCleansUpOnce(t *testing.T) {
	for _, code := range []int{0, 7} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			s := newContractSuite()
			closed := 0
			var teardown context.Context
			s.containers = append(s.containers, terminationContainer{terminate: func(ctx context.Context) error {
				teardown = ctx
				require.NoError(t, ctx.Err())
				deadline, ok := ctx.Deadline()
				require.True(t, ok)
				require.InDelta(t, 30, time.Until(deadline).Seconds(), 1)
				closed++
				return errors.New("container cleanup failed")
			}})
			search := &closingSearchFixture{}
			s.ms = search
			result := runSuite(func() int {
				require.True(t, s.enabled[serviceRedis])
				require.True(t, s.enabled[servicePostgres])
				require.True(t, s.enabled[serviceClickHouse])
				require.True(t, s.enabled[serviceMeilisearch])
				require.True(t, s.enabled[serviceKafka])
				require.Zero(t, closed)
				return code
			}, s, WithRedis(), WithPostgres(), WithClickHouse(), WithMeilisearch(), WithKafka())
			require.Equal(t, code, result)
			require.ErrorIs(t, teardown.Err(), context.Canceled)
			s.terminate(t.Context())
			require.Equal(t, 1, closed)
			require.Equal(t, 1, search.calls)
			require.Nil(t, s.ms)
		})
	}
}

func newContractSuite() *suite {
	return &suite{enabled: map[service]bool{}, started: map[service]bool{}, startErrs: map[service]error{}}
}

func TestEventuallyRetriesUntilTheConditionChanges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		start := time.Now()
		Eventually(t, time.Minute, time.Second, func() bool { calls++; return calls == 3 })
		require.Equal(t, 3, calls)
		require.Equal(t, 2*time.Second, time.Since(start))
	})
}

func TestReportingFailureAndSkipContracts(t *testing.T) {
	for _, tc := range []struct {
		mode, message string
		status        string
		short         bool
	}{
		{"lenient", "integration test skipped: fixture failed", "skip", false},
		{"strict", "integration test failed: fixture failed", "fail", false},
		{"suite disabled", "not enabled for this package", "fail", false},
		{"suite cached", "testcontainer unavailable: startup failed", "skip", false},
		{"suite failure", "failed to start postgres testcontainer: startup failed", "skip", false},
		{"suite ready", "PASS", "pass", false},
		{"suite short", "skipping integration test in -short mode", "skip", true},
		{"docker short", "skipping integration test in -short mode", "skip", true},
		{"docker invalid", "docker daemon unavailable", "skip", false},
		{"docker ping", "docker daemon unreachable", "skip", false},
		{"docker ready", "PASS", "pass", false},
		{"eventually", "condition not met", "fail", false},
		{"record timeout", "no matching kafka record", "fail", false},
		{"record error", "poll fetches: client closed", "fail", false},
		{"consumer invalid", "failed to create kafka consumer", "fail", false},
		{"consumer ready", "PASS", "pass", false},
	} {
		t.Run(tc.mode, func(t *testing.T) { reportingChild(t, tc.mode, tc.message, tc.status, tc.short) })
	}
}

func reportingChild(t *testing.T, mode, message, status string, short bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	args := []string{"-test.run=^TestReportingContractChild$", "-test.v"}
	if short {
		args = append(args, "-test.short")
	}
	if directory := flag.Lookup("test.gocoverdir"); directory != nil && directory.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+directory.Value.String())
	}
	executable, err := os.Executable()
	require.NoError(t, err)
	command := exec.CommandContext(ctx, executable, args...)
	command.Env = append(os.Environ(), "CHRONOVERSE_TESTKIT_CONTRACT="+mode, "TESTCONTAINERS_SESSION_ID="+uuid.NewString())
	output, err := command.CombinedOutput()
	require.NoError(t, ctx.Err(), "%s", output)
	switch status {
	case "pass":
		require.NoError(t, err, "%s", output)
		require.Contains(t, string(output), "--- PASS: TestReportingContractChild", "%s", output)
	case "skip":
		require.NoError(t, err, "%s", output)
		require.Contains(t, string(output), "--- SKIP: TestReportingContractChild", "%s", output)
	case "fail":
		require.Error(t, err, "%s", output)
		require.Contains(t, string(output), "--- FAIL: TestReportingContractChild", "%s", output)
	default:
		t.Fatalf("unknown child status %q", status)
	}
	require.Contains(t, string(output), message)
}

func TestReportingContractChild(t *testing.T) {
	mode := os.Getenv("CHRONOVERSE_TESTKIT_CONTRACT")
	if mode == "" {
		t.Skip("subprocess contract")
	}
	t.Setenv("TESTKIT_STRICT", "0")
	switch mode {
	case "lenient", "strict":
		if mode == "strict" {
			t.Setenv("TESTKIT_STRICT", "1")
		}
		skipOrFailf(t, "fixture %s", "failed")
	case "suite disabled", "suite cached", "suite failure", "suite ready", "suite short":
		exerciseSuiteReporting(t, mode)
	case "docker short", "docker invalid", "docker ping", "docker ready":
		exerciseDockerReporting(t, mode)
	case "eventually":
		synctest.Test(t, func(t *testing.T) { Eventually(t, time.Second, time.Millisecond, func() bool { return false }) })
	case "record timeout", "record error":
		client, err := kgo.NewClient(kgo.SeedBrokers("127.0.0.1:1"))
		require.NoError(t, err)
		t.Cleanup(client.Close)
		timeout := 10 * time.Millisecond
		if mode == "record error" {
			client.Close()
			timeout = time.Minute
		}
		WaitForRecord(t, client, timeout, func(*kgo.Record) bool { return false })
	case "consumer invalid", "consumer ready":
		shared.started[serviceKafka] = true
		shared.kafkaSeed = "127.0.0.1:1"
		topic := "records"
		if mode == "consumer invalid" {
			topic = ""
		}
		client := KafkaRecordConsumer(t, topic)
		require.NoError(t, client.Context().Err())
	case "seed user", "seed workflow":
		exerciseSeedFailure(t, mode)
	default:
		t.Fatalf("unknown contract %q", mode)
	}
}

func exerciseSuiteReporting(t *testing.T, mode string) {
	t.Helper()
	s := newContractSuite()
	cause := errors.New("startup failed")
	s.enabled[servicePostgres] = mode != "suite disabled"
	if mode == "suite cached" {
		s.startErrs[servicePostgres] = cause
	}
	var received context.Context
	calls := 0
	start := func(ctx context.Context) error {
		calls++
		received = ctx
		require.NoError(t, ctx.Err())
		if mode == "suite failure" {
			return cause
		}
		return nil
	}
	if mode == "suite failure" {
		defer func() {
			require.ErrorIs(t, s.startErrs[servicePostgres], cause)
			require.False(t, s.started[servicePostgres])
		}()
	}
	s.startWith(t, servicePostgres, start)
	s.startWith(t, servicePostgres, start)
	require.Equal(t, 1, calls)
	require.True(t, s.started[servicePostgres])
	require.ErrorIs(t, received.Err(), context.Canceled)
}

func exerciseDockerReporting(t *testing.T, mode string) {
	t.Helper()
	t.Setenv("DOCKER_TLS_VERIFY", "")
	t.Setenv("DOCKER_CERT_PATH", "")
	t.Setenv("DOCKER_API_VERSION", "")
	if mode == "docker invalid" {
		t.Setenv("DOCKER_HOST", "tcp://%")
		RequireDocker(t)
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if mode == "docker ping" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("API-Version", "1.48")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(server.URL, "http://"))
	RequireDocker(t)
}

func exerciseSeedFailure(t *testing.T, mode string) {
	t.Helper()
	t.Setenv("TESTKIT_STRICT", "1")
	s := newContractSuite()
	s.enabled[servicePostgres] = true
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s.terminate(ctx)
	})
	s.start(t, servicePostgres)
	if mode == "seed user" {
		SeedUser(t.Context(), t, s.pg, "duplicate@chronoverse.test")
		SeedUser(t.Context(), t, s.pg, "duplicate@chronoverse.test")
	} else {
		SeedWorkflow(t.Context(), t, s.pg, "00000000-0000-0000-0000-000000000000", "missing user")
	}
}

func TestIntegrationSeedFailuresAreReported(t *testing.T) {
	RequireDocker(t)
	reportingChild(t, "seed user", "seed user:", "fail", false)
	reportingChild(t, "seed workflow", "seed workflow:", "fail", false)
}
