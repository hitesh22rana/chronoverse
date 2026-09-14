package container_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

// TestIntegrationCheckImageStorageUnderLimit exercises the live DiskUsage
// reserve-check path with a limit no daemon can exceed, so no prune fires.
// The over-budget branch (prune + refuse) is intentionally not run live: it
// would prune the host daemon's unused images.
func TestIntegrationCheckImageStorageUnderLimit(t *testing.T) {
	t.Parallel()

	testkit.RequireDocker(t)

	workflow, err := container.NewDockerWorkflow(container.WithImageStorageLimit(1 << 60))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = workflow.Close()
	})

	require.NoError(t, workflow.CheckImageStorage(t.Context()))
}
