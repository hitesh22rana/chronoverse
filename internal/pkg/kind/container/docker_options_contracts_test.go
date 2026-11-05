//nolint:testpackage // Verifies constructor options through Docker requests and host configuration.
package container

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/docker/docker/api/types/network"
)

func TestDockerOptionsReachDaemonAndContainerPolicy(t *testing.T) {
	t.Parallel()
	const subnet = "198.18.246.0/24"
	server, created := optionContractDaemon(t, subnet)
	limits := ResourceLimits{MemoryBytes: 512 << 20, NanoCPUs: 1_500_000_000, PidsLimit: 128}
	workflow, err := NewDockerWorkflow(WithDockerHost(server.URL), WithResourceLimits(limits), WithWorkloadSubnet(subnet), WithWorkloadSubnet(""))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := workflow.Close(); err != nil {
			t.Error(err)
		}
	})
	if got := workflow.DockerHost(); got != server.URL {
		t.Fatalf("DockerHost=%q, want %q", got, server.URL)
	}
	policy := workflow.hostConfig()
	if policy.Resources.Memory != limits.MemoryBytes {
		t.Errorf("memory=%d", policy.Resources.Memory)
	}
	if policy.Resources.NanoCPUs != limits.NanoCPUs {
		t.Errorf("CPU=%d", policy.Resources.NanoCPUs)
	}
	if policy.Resources.PidsLimit == nil {
		t.Fatal("missing PID limit")
	}
	if *policy.Resources.PidsLimit != limits.PidsLimit {
		t.Errorf("PIDs=%d", *policy.Resources.PidsLimit)
	}
	if err := workflow.ensureWorkloadNetwork(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-created:
		if request.Name != DefaultWorkloadNetwork {
			t.Errorf("network=%q", request.Name)
		}
		if request.IPAM == nil || len(request.IPAM.Config) != 1 {
			t.Fatal("missing network subnet")
		}
		if request.IPAM.Config[0].Subnet != subnet {
			t.Errorf("subnet=%q, want %q", request.IPAM.Config[0].Subnet, subnet)
		}
	default:
		t.Fatal("configured network was not created")
	}
}

type optionContractNetwork struct {
	Name string
	IPAM *network.IPAM
}

func optionContractDaemon(t *testing.T, subnet string) (server *httptest.Server, created <-chan optionContractNetwork) {
	t.Helper()
	requests := make(chan optionContractNetwork, 1)
	var exists atomic.Bool
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.51")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/networks/"):
			if !exists.Load() {
				http.Error(w, `{"message":"network not found"}`, http.StatusNotFound)
				return
			}
			writeDockerTestResponse(t, w, `{"Name":"chronoverse-workloads","Driver":"bridge",`+
				`"Options":{"com.docker.network.bridge.enable_icc":"false","com.docker.network.bridge.name":"chronoverse-br"},`+
				`"IPAM":{"Config":[{"Subnet":"`+subnet+`"}]}}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/networks/create"):
			var request optionContractNetwork
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				http.Error(w, "bad create request", http.StatusBadRequest)
				return
			}
			requests <- request
			exists.Store(true)
			w.WriteHeader(http.StatusCreated)
			writeDockerTestResponse(t, w, `{"Id":"option-contract-network","Warning":""}`)
		default:
			t.Errorf("unexpected Docker request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, requests
}
