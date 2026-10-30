//nolint:testpackage // Tests unexported configuration validation directly.
package config

import (
	"os"
	"strings"
	"testing"

	"github.com/docker/go-units"
)

func TestExecutionWorkerWorkloadResourceValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     ExecutionWorkerConfig
		wantErr string
	}{
		{
			name: "production defaults",
			cfg:  ExecutionWorkerConfig{WorkloadMemory: "512m", WorkloadCPUs: 1, WorkloadPidsLimit: 256},
		},
		{
			name: "explicit unlimited resources",
			cfg:  ExecutionWorkerConfig{WorkloadMemory: "0"},
		},
		{
			name: "fractional memory and CPU",
			cfg:  ExecutionWorkerConfig{WorkloadMemory: "1.5GiB", WorkloadCPUs: 0.25, WorkloadPidsLimit: 17},
		},
		{
			name:    "positive CPU rounds below one nanocpu",
			cfg:     ExecutionWorkerConfig{WorkloadMemory: "1", WorkloadCPUs: 0.0000000004},
			wantErr: "invalid EXECUTION_WORKER_WORKLOAD_CONTAINER_CPUS: positive limits must round to at least one nanocpu",
		},
		{
			name: "CPU rounds up above half a nanocpu",
			cfg:  ExecutionWorkerConfig{WorkloadMemory: "1", WorkloadCPUs: 0.0000000006},
		},
		{
			name:    "missing memory",
			cfg:     ExecutionWorkerConfig{},
			wantErr: "invalid EXECUTION_WORKER_WORKLOAD_CONTAINER_MEMORY:",
		},
		{
			name:    "malformed memory",
			cfg:     ExecutionWorkerConfig{WorkloadMemory: "several megabytes"},
			wantErr: "invalid EXECUTION_WORKER_WORKLOAD_CONTAINER_MEMORY:",
		},
		{
			name:    "negative memory",
			cfg:     ExecutionWorkerConfig{WorkloadMemory: "-1m"},
			wantErr: "invalid EXECUTION_WORKER_WORKLOAD_CONTAINER_MEMORY:",
		},
		{
			name:    "negative CPU",
			cfg:     ExecutionWorkerConfig{WorkloadMemory: "512m", WorkloadCPUs: -0.5},
			wantErr: "invalid EXECUTION_WORKER_WORKLOAD_CONTAINER_CPUS: must be non-negative",
		},
		{
			name:    "negative PID limit",
			cfg:     ExecutionWorkerConfig{WorkloadMemory: "512m", WorkloadCPUs: 1, WorkloadPidsLimit: -1},
			wantErr: "invalid EXECUTION_WORKER_WORKLOAD_CONTAINER_PIDS_LIMIT: must be non-negative",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.cfg.validate()
			if tt.wantErr != "" {
				if err == nil || !strings.HasPrefix(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want prefix %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestExecutionWorkerWorkloadMemoryOverflow(t *testing.T) {
	t.Parallel()

	const memory = "8192PiB"
	parsed, err := units.RAMInBytes(memory)
	if err != nil {
		t.Fatalf("parse overflow input: %v", err)
	}
	if parsed >= 0 {
		t.Skip("RAM parser does not produce a negative overflow result on this platform")
	}

	cfg := ExecutionWorkerConfig{WorkloadMemory: memory}
	const wantErr = "invalid EXECUTION_WORKER_WORKLOAD_CONTAINER_MEMORY: must be non-negative"
	if err := cfg.validate(); err == nil || err.Error() != wantErr {
		t.Fatalf("error = %v, want %q", err, wantErr)
	}
}

func TestExecutionWorkerRejectsInvalidCPUEnvironment(t *testing.T) {
	clearExecutionWorkerEnvironment(t)
	setExecutionWorkerRequiredEnvironment(t)

	for _, tc := range []struct {
		value, message string
	}{
		{"1e-12", "positive limits must round to at least one nanocpu"},
		{"NaN", "must be finite"},
		{"+Inf", "must be finite"},
		{"-Inf", "must be finite"},
		{"9223372036.854776", "exceeds supported nanocpu range"},
		{"1e308", "exceeds supported nanocpu range"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("EXECUTION_WORKER_WORKLOAD_CONTAINER_CPUS", tc.value)
			t.Setenv("EXECUTION_WORKER_WORKLOAD_CONTAINER_MEMORY", "512m")
			t.Setenv("EXECUTION_WORKER_WORKLOAD_CONTAINER_PIDS_LIMIT", "256")

			_, err := InitExecutionJobConfig()
			want := "invalid EXECUTION_WORKER_WORKLOAD_CONTAINER_CPUS: " + tc.message
			if err == nil || err.Error() != want {
				t.Fatalf("error = %v, want %q", err, want)
			}
		})
	}
}

func TestExecutionWorkerAcceptsValidWorkloadEnvironment(t *testing.T) {
	clearExecutionWorkerEnvironment(t)
	setExecutionWorkerRequiredEnvironment(t)

	t.Setenv("EXECUTION_WORKER_WORKLOAD_CONTAINER_CPUS", "0.25")
	t.Setenv("EXECUTION_WORKER_WORKLOAD_CONTAINER_MEMORY", "512m")
	t.Setenv("EXECUTION_WORKER_WORKLOAD_CONTAINER_PIDS_LIMIT", "256")

	cfg, err := InitExecutionJobConfig()
	if err != nil {
		t.Fatalf("InitExecutionJobConfig() error = %v", err)
	}
	if cfg.WorkloadCPUs != 0.25 {
		t.Fatalf("WorkloadCPUs = %v, want 0.25", cfg.WorkloadCPUs)
	}
	if cfg.WorkerID == "" {
		t.Fatal("WorkerID is empty")
	}
}

func TestExecutionWorkerKeepsConfiguredWorkerID(t *testing.T) {
	clearExecutionWorkerEnvironment(t)
	setExecutionWorkerRequiredEnvironment(t)

	t.Setenv("EXECUTION_WORKER_ID", "worker-a")

	cfg, err := InitExecutionJobConfig()
	if err != nil {
		t.Fatalf("InitExecutionJobConfig() error = %v", err)
	}
	if cfg.WorkerID != "worker-a" {
		t.Fatalf("WorkerID = %q, want %q", cfg.WorkerID, "worker-a")
	}
}

func TestExecutionWorkerRejectsInvalidEnvironment(t *testing.T) {
	clearExecutionWorkerEnvironment(t)
	setExecutionWorkerRequiredEnvironment(t)

	t.Setenv("EXECUTION_WORKER_CONCURRENCY", "not-an-int")

	_, err := InitExecutionJobConfig()
	if err == nil || !strings.Contains(err.Error(), "EXECUTION_WORKER_CONCURRENCY") {
		t.Fatalf("error = %v, want EXECUTION_WORKER_CONCURRENCY parse error", err)
	}
}

func setExecutionWorkerRequiredEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("KAFKA_BROKERS", "localhost:9092")
	t.Setenv("WORKFLOWS_SERVICE_HOST", "localhost")
	t.Setenv("WORKFLOWS_SERVICE_PORT", "50051")
	t.Setenv("JOBS_SERVICE_HOST", "localhost")
	t.Setenv("JOBS_SERVICE_PORT", "50052")
	t.Setenv("NOTIFICATIONS_SERVICE_HOST", "localhost")
	t.Setenv("NOTIFICATIONS_SERVICE_PORT", "50053")
}

func clearExecutionWorkerEnvironment(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "EXECUTION_WORKER_") {
			t.Setenv(key, "")
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
	}
}
