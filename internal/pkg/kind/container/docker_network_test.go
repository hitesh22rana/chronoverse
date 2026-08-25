//nolint:testpackage // Tests fail-closed validation around the unexported network ensure helper.
package container

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestValidateWorkloadNetworkNameRejectsReservedModes(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", " bridge", "bridge", "HOST", "none", "default", "nat", "container", "container:peer", platformNetwork} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := validateWorkloadNetworkName(name); status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("validateWorkloadNetworkName(%q) code = %s, want %s: %v", name, status.Code(err), codes.FailedPrecondition, err)
			}
		})
	}
}

func TestValidateWorkloadNetworkRequiresIsolatedBridge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		inspected network.Inspect
		wantCode  codes.Code
	}{
		{
			name: "isolated bridge",
			inspected: network.Inspect{
				Name:    DefaultWorkloadNetwork,
				Driver:  "bridge",
				Options: map[string]string{workloadNetworkICCOption: "false"},
			},
			wantCode: codes.OK,
		},
		{
			name: "unexpected name",
			inspected: network.Inspect{
				Name:    "other-workloads",
				Driver:  "bridge",
				Options: map[string]string{workloadNetworkICCOption: "false"},
			},
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "platform network",
			inspected: network.Inspect{
				Name:    platformNetwork,
				Driver:  "bridge",
				Options: map[string]string{workloadNetworkICCOption: "false"},
			},
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "overlay driver",
			inspected: network.Inspect{
				Name:    DefaultWorkloadNetwork,
				Driver:  "overlay",
				Options: map[string]string{workloadNetworkICCOption: "false"},
			},
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "icc enabled",
			inspected: network.Inspect{
				Name:    DefaultWorkloadNetwork,
				Driver:  "bridge",
				Options: map[string]string{workloadNetworkICCOption: "true"},
			},
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "icc option absent",
			inspected: network.Inspect{
				Name:   DefaultWorkloadNetwork,
				Driver: "bridge",
			},
			wantCode: codes.FailedPrecondition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateWorkloadNetwork(DefaultWorkloadNetwork, &tt.inspected)
			if status.Code(err) != tt.wantCode {
				t.Fatalf("validateWorkloadNetwork() code = %s, want %s: %v", status.Code(err), tt.wantCode, err)
			}
		})
	}
}

func TestEnsureWorkloadNetworkValidatesExistingNetwork(t *testing.T) {
	t.Parallel()

	var createCalls atomic.Int32
	workflow := newNetworkTestWorkflow(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			createCalls.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		writeDockerTestResponse(t, w, `{"Name":"chronoverse-workloads","Driver":"bridge","Options":{"com.docker.network.bridge.enable_icc":"true"}}`)
	}))

	err := workflow.ensureWorkloadNetwork(context.Background())
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ensureWorkloadNetwork() code = %s, want %s: %v", status.Code(err), codes.FailedPrecondition, err)
	}
	if got := createCalls.Load(); got != 0 {
		t.Fatalf("network create calls = %d, want 0", got)
	}
}

func TestEnsureWorkloadNetworkValidatesCreatedNetwork(t *testing.T) {
	t.Parallel()

	var inspectCalls atomic.Int32
	workflow := newNetworkTestWorkflow(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/networks/"):
			if inspectCalls.Add(1) == 1 {
				w.WriteHeader(http.StatusNotFound)
				writeDockerTestResponse(t, w, `{"message":"network not found"}`)
				return
			}
			writeDockerTestResponse(t, w, `{"Name":"chronoverse-workloads","Driver":"bridge","Options":{"com.docker.network.bridge.enable_icc":"false"}}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/networks/create"):
			w.WriteHeader(http.StatusCreated)
			writeDockerTestResponse(t, w, `{"Id":"network-id"}`)
		default:
			t.Fatalf("unexpected Docker API request: %s %s", r.Method, r.URL.String())
		}
	}))

	if err := workflow.ensureWorkloadNetwork(context.Background()); err != nil {
		t.Fatalf("ensureWorkloadNetwork() error = %v", err)
	}
	if got := inspectCalls.Load(); got != 2 {
		t.Fatalf("network inspect calls = %d, want 2", got)
	}
}

func TestEnsureWorkloadNetworkValidatesRaceWinner(t *testing.T) {
	t.Parallel()

	var inspectCalls atomic.Int32
	workflow := newNetworkTestWorkflow(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/networks/"):
			if inspectCalls.Add(1) == 1 {
				w.WriteHeader(http.StatusNotFound)
				writeDockerTestResponse(t, w, `{"message":"network not found"}`)
				return
			}
			writeDockerTestResponse(t, w, `{"Name":"chronoverse-workloads","Driver":"bridge","Options":{"com.docker.network.bridge.enable_icc":"true"}}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/networks/create"):
			w.WriteHeader(http.StatusConflict)
			writeDockerTestResponse(t, w, `{"message":"network already exists"}`)
		default:
			t.Fatalf("unexpected Docker API request: %s %s", r.Method, r.URL.String())
		}
	}))

	err := workflow.ensureWorkloadNetwork(context.Background())
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ensureWorkloadNetwork() code = %s, want %s: %v", status.Code(err), codes.FailedPrecondition, err)
	}
}

func newNetworkTestWorkflow(t *testing.T, handler http.Handler) *DockerWorkflow {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.51"))
	if err != nil {
		t.Fatalf("create Docker client: %v", err)
	}
	t.Cleanup(func() {
		_ = cli.Close()
	})

	return &DockerWorkflow{Client: cli, workloadNetwork: DefaultWorkloadNetwork}
}
