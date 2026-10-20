package main

import (
	"strings"
	"testing"

	"github.com/docker/go-units"

	"github.com/hitesh22rana/chronoverse/internal/config"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
)

func TestWorkloadResourceLimits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     *config.ExecutionWorkerConfig
		want    container.ResourceLimits
		wantErr string
	}{
		{name: "nil configuration"},
		{
			name: "production defaults",
			cfg:  &config.ExecutionWorkerConfig{WorkloadMemory: "512m", WorkloadCPUs: 1, WorkloadPidsLimit: 256},
			want: container.ResourceLimits{MemoryBytes: 536870912, NanoCPUs: 1000000000, PidsLimit: 256},
		},
		{
			name: "explicit unlimited resources",
			cfg:  &config.ExecutionWorkerConfig{WorkloadMemory: "0"},
		},
		{
			name: "fractional memory and CPU",
			cfg:  &config.ExecutionWorkerConfig{WorkloadMemory: "1.5GiB", WorkloadCPUs: 0.25, WorkloadPidsLimit: 17},
			want: container.ResourceLimits{MemoryBytes: 1610612736, NanoCPUs: 250000000, PidsLimit: 17},
		},
		{
			name: "CPU rounds down below half a nanocpu",
			cfg:  &config.ExecutionWorkerConfig{WorkloadMemory: "1", WorkloadCPUs: 0.0000000004},
			want: container.ResourceLimits{MemoryBytes: 1},
		},
		{
			name: "CPU rounds up above half a nanocpu",
			cfg:  &config.ExecutionWorkerConfig{WorkloadMemory: "1", WorkloadCPUs: 0.0000000006},
			want: container.ResourceLimits{MemoryBytes: 1, NanoCPUs: 1},
		},
		{
			name:    "missing memory",
			cfg:     &config.ExecutionWorkerConfig{},
			wantErr: "invalid EXECUTION_WORKER_WORKLOAD_CONTAINER_MEMORY:",
		},
		{
			name:    "malformed memory",
			cfg:     &config.ExecutionWorkerConfig{WorkloadMemory: "several megabytes"},
			wantErr: "invalid EXECUTION_WORKER_WORKLOAD_CONTAINER_MEMORY:",
		},
		{
			name:    "negative memory",
			cfg:     &config.ExecutionWorkerConfig{WorkloadMemory: "-1m"},
			wantErr: "invalid EXECUTION_WORKER_WORKLOAD_CONTAINER_MEMORY:",
		},
		{
			name:    "negative CPU",
			cfg:     &config.ExecutionWorkerConfig{WorkloadMemory: "512m", WorkloadCPUs: -0.5},
			wantErr: "invalid EXECUTION_WORKER_WORKLOAD_CONTAINER_CPUS: must be non-negative",
		},
		{
			name:    "negative PID limit",
			cfg:     &config.ExecutionWorkerConfig{WorkloadMemory: "512m", WorkloadCPUs: 1, WorkloadPidsLimit: -1},
			wantErr: "invalid EXECUTION_WORKER_WORKLOAD_CONTAINER_PIDS_LIMIT: must be non-negative",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := workloadResourceLimits(tt.cfg)
			if tt.wantErr != "" {
				if err == nil || !strings.HasPrefix(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want prefix %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("limits = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestWorkloadResourceLimitsRejectsNegativeParserOverflow(t *testing.T) {
	t.Parallel()

	const memory = "8192PiB"
	parsed, err := units.RAMInBytes(memory)
	if err != nil {
		t.Fatalf("parse overflow input: %v", err)
	}
	if parsed >= 0 {
		t.Skip("RAM parser does not produce a negative overflow result on this platform")
	}

	got, err := workloadResourceLimits(&config.ExecutionWorkerConfig{WorkloadMemory: memory})
	const wantErr = "invalid EXECUTION_WORKER_WORKLOAD_CONTAINER_MEMORY: must be non-negative"
	if err == nil || err.Error() != wantErr {
		t.Fatalf("error = %v, want %q", err, wantErr)
	}
	if got != (container.ResourceLimits{}) {
		t.Errorf("limits = %+v, want no partial limits", got)
	}
}
