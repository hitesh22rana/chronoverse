package main

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/hitesh22rana/chronoverse/internal/config"
)

func TestRunRejectsInvalidConfiguration(t *testing.T) {
	if os.Getenv("CHRONOVERSE_COMMAND_CONFIG_CHILD") == "1" {
		if _, err := config.InitSchedulingJobConfig(); err != nil {
			t.Fatalf("valid startup fixture rejected before injecting failure: %v", err)
		}
		t.Setenv("POSTGRES_PORT", "not-a-port")
		if code := run(); code != ExitError {
			t.Fatalf("invalid configuration exit = %d, want %d", code, ExitError)
		}
		return
	}

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	args := []string{"-test.run=^TestRunRejectsInvalidConfiguration$"}
	if coverageDir := flag.Lookup("test.gocoverdir"); coverageDir != nil && coverageDir.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+coverageDir.Value.String())
	}
	//nolint:gosec // Executes this test binary with fixed arguments and isolated startup configuration.
	child := exec.CommandContext(ctx, os.Args[0], args...)
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "TMP", "TEMP", "GOCOVERDIR", "GORACE", "GODEBUG"} {
		if value, ok := os.LookupEnv(key); ok {
			child.Env = append(child.Env, key+"="+value)
		}
	}
	child.Env = append(child.Env,
		"CHRONOVERSE_COMMAND_CONFIG_CHILD=1",
		"ENV=development",
		"CRYPTO_SECRET="+strings.Repeat("a", 32),
		"SERVER_CSRF_HMAC_SECRET="+strings.Repeat("b", 32),
		"GRPC_PORT=8080",
		"KAFKA_BROKERS=localhost:9092",
		"MEILISEARCH_MASTER_KEY=command-startup-fixture",
		"USERS_SERVICE_HOST=localhost", "USERS_SERVICE_PORT=8081",
		"WORKFLOWS_SERVICE_HOST=localhost", "WORKFLOWS_SERVICE_PORT=8082",
		"JOBS_SERVICE_HOST=localhost", "JOBS_SERVICE_PORT=8083",
		"NOTIFICATIONS_SERVICE_HOST=localhost", "NOTIFICATIONS_SERVICE_PORT=8084",
		"ANALYTICS_SERVICE_HOST=localhost", "ANALYTICS_SERVICE_PORT=8085",
		"OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:1",
		"OTEL_EXPORTER_OTLP_TIMEOUT=1",
		"OTEL_METRIC_EXPORT_INTERVAL=3600000",
	)
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("configuration rejection subprocess failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "POSTGRES_PORT") {
		t.Fatalf("startup lost original configuration error: %s", output)
	}
}
