package svc

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestInitSignalCancellation(t *testing.T) {
	if os.Getenv("SVC_SIGNAL_CHILD") == "1" {
		clearTelemetryEnvironment(t)
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
		t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "1")
		ctx, closeService := Init()
		defer closeService()
		if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			if ctx.Err() != context.Canceled {
				t.Fatalf("unexpected signal cancellation: %v", ctx.Err())
			}
		case <-time.After(2 * time.Second):
			t.Fatal("SIGTERM did not cancel service")
		}
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	//nolint:gosec // Runs this test binary with fixed helper flags.
	cmd := exec.CommandContext(ctx, os.Args[0], serviceChildArgs("TestInitSignalCancellation")...)
	cmd.Env = serviceChildEnvironment("SVC_SIGNAL_CHILD=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("service signal child failed: %v\n%s", err, output)
	}
}

func TestInitFailureRestoresDefaultSignalBehavior(t *testing.T) {
	if os.Getenv("SVC_INIT_FAILURE_CHILD") == "1" {
		clearTelemetryEnvironment(t)
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "missing-equals")
		func() {
			defer func() {
				if recovered := recover(); recovered == nil || !strings.Contains(fmt.Sprint(recovered), "resource") {
					t.Fatalf("expected resource initialization failure, got %v", recovered)
				}
			}()
			Init()
		}()
		if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Second)
		t.Fatal("failed initialization left SIGTERM intercepted")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	//nolint:gosec // Runs this test binary with fixed helper flags.
	cmd := exec.CommandContext(ctx, os.Args[0], serviceChildArgs("TestInitFailureRestoresDefaultSignalBehavior")...)
	cmd.Env = serviceChildEnvironment("SVC_INIT_FAILURE_CHILD=1")
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("expected SIGTERM exit after rollback, got %v\n%s", err, output)
	}
	status, ok := exit.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGTERM {
		t.Fatalf("expected default SIGTERM behavior after failure, got %v\n%s", exit.ProcessState, output)
	}
}

func serviceChildEnvironment(marker string) []string {
	environment := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "OTEL_") && !strings.HasPrefix(key, "SVC_") {
			environment = append(environment, entry)
		}
	}
	return append(environment, marker)
}

func serviceChildArgs(name string) []string {
	args := []string{"-test.run=^" + name + "$"}
	if coverageDir := flag.Lookup("test.gocoverdir"); coverageDir != nil && coverageDir.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+coverageDir.Value.String())
	}
	return args
}
