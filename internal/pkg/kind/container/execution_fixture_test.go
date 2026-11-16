//nolint:testpackage // Adapter stream fixtures require the package-private watcher.
package container

import (
	"context"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
)

// ExecuteTestFixture only sets up adapter stream/host-config tests. Production
// execution requires durable Create and Start grants; this helper is test-only.
//
//nolint:gocritic // Preserve the established Execute result signature.
func (w *DockerWorkflow) ExecuteTestFixture(
	ctx context.Context,
	timeout time.Duration,
	image string,
	cmd []string,
	env []string,
) (string, <-chan *jobsmodel.JobLog, <-chan error, error) {
	if err := w.healthCheck(ctx); err != nil {
		return "", nil, nil, err
	}

	containerTimeout := int(timeout.Seconds())

	createContainer := func() (client.ContainerCreateResult, error) {
		return w.Client.ContainerCreate(
			ctx,
			client.ContainerCreateOptions{
				Config: &container.Config{
					Image:       image,
					Cmd:         cmd,
					StopTimeout: &containerTimeout,
					Env:         env,
				},
				HostConfig: w.hostConfig(),
			},
		)
	}

	// Cached clients outlive the network they initialized: revalidate before
	// use so prunes are recreated and unsafe replacements are rejected before
	// Docker attaches a workload. Drift is infra, not user error, hence the
	// retryable Internal classification.
	if err := w.ensureWorkloadNetwork(ctx); err != nil {
		return "", nil, nil, status.Errorf(codes.Internal, "workload network is not ready: %v", err)
	}

	resp, err := createContainer()
	if err != nil || resp.ID == "" {
		return "", nil, nil, status.Errorf(codes.FailedPrecondition, "failed to create container: %v", err)
	}

	containerID := resp.ID

	if _, err := w.Client.ContainerStart(ctx, containerID, client.ContainerStartOptions{}); err != nil {
		return containerID, nil, nil, status.Errorf(codes.Aborted, "failed to start container: %v", err)
	}

	logs, errs := w.watchContainer(ctx, timeout, containerID)
	return containerID, logs, errs, nil
}
