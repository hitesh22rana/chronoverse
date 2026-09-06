package executor

import (
	"context"

	"github.com/hitesh22rana/chronoverse/internal/pkg/imagepull"
)

// NewImagePullLockedContainerSvc wraps a container service with Redis-backed image pull coordination.
func NewImagePullLockedContainerSvc(inner ContainerSvc, locks imagepull.LockStore, cfg imagepull.Config) ContainerSvc {
	return &imagePullLockedContainerSvc{
		ContainerSvc: inner,
		locks:        locks,
		cfg:          cfg,
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
