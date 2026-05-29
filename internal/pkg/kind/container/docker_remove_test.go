package container_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"

	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
)

func TestDockerWorkflowRemoveIgnoresRemovalInProgress(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.True(t, strings.HasSuffix(r.URL.Path, "/containers/container-1"))

		w.WriteHeader(http.StatusConflict)
		_, err := w.Write([]byte(`{"message":"removal of container container-1 is already in progress"}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	cli, err := client.NewClientWithOpts(client.WithHost(server.URL))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cli.Close())
	})

	workflow := &container.DockerWorkflow{
		Client: cli,
	}

	require.NoError(t, workflow.Remove(context.Background(), "container-1"))
}
