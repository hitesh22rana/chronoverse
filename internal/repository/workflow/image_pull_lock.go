package workflow

import (
	"context"
	"time"

	"github.com/hitesh22rana/chronoverse/internal/pkg/imagepull"
)

// ImagePullLockConfig configures distributed Docker image pull coordination.
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

func (s *imagePullLockedContainerSvc) ResolveImageDigest(ctx context.Context, imageName string) (resolvedImageRef, resolvedImageDigest string, err error) {
	if err := s.Build(ctx, imageName); err != nil {
		return imageName, "", err
	}
	return s.ContainerSvc.ResolveImageDigest(ctx, imageName)
}
