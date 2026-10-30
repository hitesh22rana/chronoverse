package config_test

import (
	"os"
	"strings"
	"testing"

	"github.com/hitesh22rana/chronoverse/internal/config"
)

func TestExecutionWorkerRejectsInvalidWorkloadEnvironment(t *testing.T) {
	clearExecutionWorkerEnvironment(t)
	setExecutionWorkerRequiredEnvironment(t)

	for _, tc := range []struct {
		name, key, value, message string
	}{
		{"empty memory", "MEMORY", "", ""},
		{"malformed memory", "MEMORY", "several megabytes", ""},
		{"negative memory", "MEMORY", "-1m", ""},
		{"overflowing memory", "MEMORY", "8192PiB", ""},
		{"memory above overflow boundary", "MEMORY", "16384PiB", ""},
		{"negative CPU", "CPUS", "-0.5", "must be non-negative"},
		{"CPU below rounding boundary", "CPUS", "0.0000000004999999999", "positive limits must round to at least one nanocpu"},
		{"NaN CPU", "CPUS", "NaN", "must be finite"},
		{"positive infinite CPU", "CPUS", "+Inf", "must be finite"},
		{"negative infinite CPU", "CPUS", "-Inf", "must be finite"},
		{"CPU at overflow boundary", "CPUS", "9223372036.854776", "exceeds supported nanocpu range"},
		{"CPU multiplication overflows", "CPUS", "1e308", "exceeds supported nanocpu range"},
		{"negative PID limit", "PIDS_LIMIT", "-1", "must be non-negative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("EXECUTION_WORKER_WORKLOAD_CONTAINER_"+tc.key, tc.value)

			cfg, err := config.InitExecutionJobConfig()
			want := "invalid EXECUTION_WORKER_WORKLOAD_CONTAINER_" + tc.key + ": " + tc.message
			if err == nil || !strings.HasPrefix(err.Error(), want) {
				t.Fatalf("error = %v, want prefix %q", err, want)
			}
			if cfg != nil {
				t.Fatalf("configuration = %+v, want nil on validation failure", cfg.ExecutionWorkerConfig)
			}
		})
	}
}

func TestExecutionWorkerAcceptsValidWorkloadEnvironment(t *testing.T) {
	clearExecutionWorkerEnvironment(t)
	setExecutionWorkerRequiredEnvironment(t)

	for _, tc := range []struct {
		name   string
		env    map[string]string
		memory string
		cpus   float64
		pids   int64
	}{
		{name: "production defaults", memory: "512m", cpus: 1, pids: 256},
		{
			name:   "explicit unlimited resources",
			env:    map[string]string{"MEMORY": "0", "CPUS": "0", "PIDS_LIMIT": "0"},
			memory: "0",
		},
		{
			name:   "fractional overrides",
			env:    map[string]string{"MEMORY": "1.5GiB", "CPUS": "0.25", "PIDS_LIMIT": "17"},
			memory: "1.5GiB", cpus: 0.25, pids: 17,
		},
		{
			name:   "CPU at rounding boundary",
			env:    map[string]string{"CPUS": "0.0000000005"},
			memory: "512m", cpus: 0.0000000005, pids: 256,
		},
		{
			name:   "CPU below overflow boundary",
			env:    map[string]string{"CPUS": "9223372036.854774"},
			memory: "512m", cpus: 9223372036.854774, pids: 256,
		},
		{
			name:   "memory below overflow boundary",
			env:    map[string]string{"MEMORY": "9223372036854774784"},
			memory: "9223372036854774784", cpus: 1, pids: 256,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for key, value := range tc.env {
				t.Setenv("EXECUTION_WORKER_WORKLOAD_CONTAINER_"+key, value)
			}
			cfg, err := config.InitExecutionJobConfig()
			if err != nil {
				t.Fatalf("InitExecutionJobConfig() error = %v", err)
			}
			if cfg.WorkloadMemory != tc.memory || cfg.WorkloadCPUs != tc.cpus || cfg.WorkloadPidsLimit != tc.pids {
				t.Errorf("limits = (%q, %v, %d), want (%q, %v, %d)",
					cfg.WorkloadMemory, cfg.WorkloadCPUs, cfg.WorkloadPidsLimit, tc.memory, tc.cpus, tc.pids)
			}
			workerID, err := os.Hostname()
			if err != nil || workerID == "" {
				workerID = "execution-worker"
			}
			if cfg.WorkerID != workerID {
				t.Errorf("WorkerID = %q, want %q", cfg.WorkerID, workerID)
			}
		})
	}
}

func TestExecutionWorkerKeepsConfiguredWorkerID(t *testing.T) {
	clearExecutionWorkerEnvironment(t)
	setExecutionWorkerRequiredEnvironment(t)

	t.Setenv("EXECUTION_WORKER_ID", "worker-a")

	cfg, err := config.InitExecutionJobConfig()
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

	_, err := config.InitExecutionJobConfig()
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
