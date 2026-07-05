package executor

import (
	"context"
	"time"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/imagepull"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
)

// ImagePullLockConfig configures runtime-local Docker image pull coordination.
type ImagePullLockConfig struct {
	TTL           time.Duration
	WaitTimeout   time.Duration
	RetryInterval time.Duration
	LockScope     string
}

// NewImagePullLockedContainerSvc wraps a container service with Redis-backed image pull coordination.
func NewImagePullLockedContainerSvc(inner ContainerSvc, locks imagepull.LockStore, cfg ImagePullLockConfig) ContainerSvc {
	return &imagePullLockedContainerSvc{
		inner: inner,
		locks: locks,
		cfg: imagepull.Config{
			TTL:           cfg.TTL,
			WaitTimeout:   cfg.WaitTimeout,
			RetryInterval: cfg.RetryInterval,
			LockScope:     cfg.LockScope,
		},
	}
}

type imagePullLockedContainerSvc struct {
	inner ContainerSvc
	locks imagepull.LockStore
	cfg   imagepull.Config
}

func (s *imagePullLockedContainerSvc) Build(ctx context.Context, imageName string) error {
	return imagepull.Ensure(ctx, s.inner, s.locks, imageName, s.cfg)
}

func (s *imagePullLockedContainerSvc) ImageExists(ctx context.Context, imageName string) (bool, error) {
	return s.inner.ImageExists(ctx, imageName)
}

func (s *imagePullLockedContainerSvc) DockerHost() string {
	return s.inner.DockerHost()
}

func (s *imagePullLockedContainerSvc) Execute(
	ctx context.Context,
	timeout time.Duration,
	image string,
	cmd,
	env []string,
) (containerID string, logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	return s.inner.Execute(ctx, timeout, image, cmd, env)
}

func (s *imagePullLockedContainerSvc) Logs(ctx context.Context, containerID string) (logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	return s.inner.Logs(ctx, containerID)
}

func (s *imagePullLockedContainerSvc) Inspect(ctx context.Context, containerID string) (*container.State, error) {
	return s.inner.Inspect(ctx, containerID)
}

func (s *imagePullLockedContainerSvc) Remove(ctx context.Context, containerID string) error {
	return s.inner.Remove(ctx, containerID)
}

func (s *imagePullLockedContainerSvc) Terminate(ctx context.Context, containerID string) error {
	return s.inner.Terminate(ctx, containerID)
}
