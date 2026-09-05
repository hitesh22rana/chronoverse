package executor

import (
	"context"
	"time"

	"github.com/hitesh22rana/chronoverse/internal/pkg/imagepull"
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
		ContainerSvc: inner,
		locks:        locks,
		cfg:          imagepull.Config(cfg),
	}
}

type imagePullLockedContainerSvc struct {
	ContainerSvc
	locks imagepull.LockStore
	cfg   imagepull.Config
}

func (s *imagePullLockedContainerSvc) Build(ctx context.Context, imageName string) error {
	return imagepull.Ensure(ctx, s.ContainerSvc, s.locks, imageName, s.cfg)
}
