//nolint:testpackage // Tests unexported DockerWorkflow resource limit wiring.
package container

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dockerclient "github.com/docker/docker/client"
	"github.com/stretchr/testify/require"
)

func TestDockerWorkflowExecuteAppliesResourceLimits(t *testing.T) {
	t.Parallel()

	var createCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.HasSuffix(r.URL.Path, "/_ping"):
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/networks/"):
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"Name":"chronoverse-workloads","Driver":"bridge","Options":{"com.docker.network.bridge.enable_icc":"false"}}`))
			require.NoError(t, err)
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/create"):
			createCalled = true
			var req struct {
				HostConfig struct {
					Memory    int64  `json:"Memory"`
					NanoCPUs  int64  `json:"NanoCpus"`
					PidsLimit *int64 `json:"PidsLimit"`
				} `json:"HostConfig"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			require.Equal(t, int64(512*1024*1024), req.HostConfig.Memory)
			require.Equal(t, int64(1_500_000_000), req.HostConfig.NanoCPUs)
			require.NotNil(t, req.HostConfig.PidsLimit)
			require.Equal(t, int64(128), *req.HostConfig.PidsLimit)

			w.WriteHeader(http.StatusCreated)
			_, err := w.Write([]byte(`{"Id":"container-1","Warnings":[]}`))
			require.NoError(t, err)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/containers/container-1/start"):
			http.Error(w, "stop before runtime goroutines start", http.StatusInternalServerError)
		default:
			t.Fatalf("unexpected docker API request: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost(server.URL))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cli.Close())
	})

	workflow := &DockerWorkflow{
		Client:          cli,
		workloadNetwork: DefaultWorkloadNetwork,
		resourceLimits: ResourceLimits{
			MemoryBytes: 512 * 1024 * 1024,
			NanoCPUs:    1_500_000_000,
			PidsLimit:   128,
		},
	}

	//nolint:dogsled // Ignore returned values, we only care about the error and createCalled.
	_, _, _, err = workflow.Execute(context.Background(), time.Second, "alpine:3.22.2", []string{"true"}, nil)
	require.Error(t, err)
	require.True(t, createCalled)
}
