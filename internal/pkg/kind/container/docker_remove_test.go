package container_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
)

func TestDockerWorkflowRemoveRetriesRemovalInProgress(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.True(t, strings.HasSuffix(r.URL.Path, "/containers/"+strings.Repeat("a", 64)))

		w.WriteHeader(http.StatusConflict)
		_, err := w.Write([]byte(`{"message":"removal of container container-1 is already in progress"}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	cli, err := client.New(client.WithHost(server.URL), client.WithAPIVersion("1.51"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cli.Close())
	})

	workflow := &container.DockerWorkflow{
		Client: cli,
	}

	require.Equal(t, codes.Aborted, status.Code(workflow.Remove(context.Background(), strings.Repeat("a", 64))))
}
