package main

import (
	"strings"
	"testing"

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
			name: "CPU rounds up at half a nanocpu",
			cfg:  &config.ExecutionWorkerConfig{WorkloadMemory: "1", WorkloadCPUs: 0.0000000005},
			want: container.ResourceLimits{MemoryBytes: 1, NanoCPUs: 1},
		},
		{
			name: "CPU rounds up above half a nanocpu",
			cfg:  &config.ExecutionWorkerConfig{WorkloadMemory: "1", WorkloadCPUs: 0.0000000006},
			want: container.ResourceLimits{MemoryBytes: 1, NanoCPUs: 1},
		},
		{
			name: "CPU rounds down below the next half nanocpu",
			cfg:  &config.ExecutionWorkerConfig{WorkloadMemory: "1", WorkloadCPUs: 0.0000000014},
			want: container.ResourceLimits{MemoryBytes: 1, NanoCPUs: 1},
		},
		{
			name: "CPU below overflow boundary",
			cfg:  &config.ExecutionWorkerConfig{WorkloadMemory: "1", WorkloadCPUs: 9223372036.854774},
			want: container.ResourceLimits{MemoryBytes: 1, NanoCPUs: 9223372036854774784},
		},
		{
			name: "memory below overflow boundary",
			cfg:  &config.ExecutionWorkerConfig{WorkloadMemory: "9223372036854774784"},
			want: container.ResourceLimits{MemoryBytes: 9223372036854774784},
		},
		{
			name:    "malformed memory",
			cfg:     &config.ExecutionWorkerConfig{WorkloadMemory: "several megabytes"},
			wantErr: "invalid EXECUTION_WORKER_WORKLOAD_CONTAINER_MEMORY:",
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
