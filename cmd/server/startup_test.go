package main

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestRunRejectsInvalidConfiguration(t *testing.T) {
	if os.Getenv("CHRONOVERSE_COMMAND_CONFIG_CHILD") == "1" {
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
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "OTEL_") && key != "CHRONOVERSE_COMMAND_CONFIG_CHILD" &&
			key != "CRYPTO_SECRET" && key != "SERVER_CSRF_HMAC_SECRET" && key != "SERVER_PORT" {
			child.Env = append(child.Env, entry)
		}
	}
	child.Env = append(child.Env,
		"CHRONOVERSE_COMMAND_CONFIG_CHILD=1",
		"CRYPTO_SECRET="+strings.Repeat("a", 32),
		"SERVER_CSRF_HMAC_SECRET="+strings.Repeat("b", 32),
		"SERVER_PORT=not-a-port",
		"OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:1",
		"OTEL_EXPORTER_OTLP_TIMEOUT=1",
		"OTEL_METRIC_EXPORT_INTERVAL=3600000",
	)
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("configuration rejection subprocess failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "SERVER_PORT") {
		t.Fatalf("startup lost original configuration error: %s", output)
	}
}
