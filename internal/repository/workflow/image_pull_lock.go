package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
)

const (
	defaultImagePullLockTTL           = 10 * time.Minute
	defaultImagePullLockWaitTimeout   = 10 * time.Minute
	defaultImagePullLockRetryInterval = 500 * time.Millisecond
	minImagePullLockRenewInterval     = time.Second
)

type imagePullLockStore interface {
	AcquireDistributedLockWithToken(ctx context.Context, key string, expiration time.Duration) (string, bool, error)
	ExtendDistributedLockWithToken(ctx context.Context, key, token string, expiration time.Duration) (bool, error)
	ReleaseDistributedLockWithToken(ctx context.Context, key, token string) error
}

// ImagePullLockConfig configures distributed Docker image pull coordination.
type ImagePullLockConfig struct {
	TTL           time.Duration
	WaitTimeout   time.Duration
	RetryInterval time.Duration
}

// NewImagePullLockedContainerSvc wraps a container service with Redis-backed image pull coordination.
func NewImagePullLockedContainerSvc(inner ContainerSvc, locks imagePullLockStore, cfg ImagePullLockConfig) ContainerSvc {
	return &imagePullLockedContainerSvc{
		inner: inner,
		locks: locks,
		cfg:   normalizeImagePullLockConfig(cfg),
	}
}

type imagePullLockedContainerSvc struct {
	inner ContainerSvc
	locks imagePullLockStore
	cfg   ImagePullLockConfig
}

func normalizeImagePullLockConfig(cfg ImagePullLockConfig) ImagePullLockConfig {
	if cfg.TTL <= 0 {
		cfg.TTL = defaultImagePullLockTTL
	}
	if cfg.WaitTimeout <= 0 {
		cfg.WaitTimeout = defaultImagePullLockWaitTimeout
	}
	if cfg.RetryInterval <= 0 {
		cfg.RetryInterval = defaultImagePullLockRetryInterval
	}
	return cfg
}

func (s *imagePullLockedContainerSvc) Build(ctx context.Context, imageName string) error {
	exists, err := s.inner.ImageExists(ctx, imageName)
	if err != nil || exists {
		return err
	}

	lockKey := imagePullLockKey(s.inner.DockerHost(), imageName)
	waitCtx, cancel := context.WithTimeout(ctx, s.cfg.WaitTimeout)
	defer cancel()

	for {
		token, acquired, err := s.locks.AcquireDistributedLockWithToken(waitCtx, lockKey, s.cfg.TTL)
		if err != nil {
			return err
		}
		if acquired {
			return s.buildWithLock(ctx, imageName, lockKey, token)
		}

		if err := waitForImagePullLock(waitCtx, s.cfg.RetryInterval); err != nil {
			return imagePullLockWaitError(ctx, err)
		}
	}
}

func (s *imagePullLockedContainerSvc) buildWithLock(ctx context.Context, imageName, lockKey, token string) error {
	buildCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stopRenewal, renewalErrCh := s.startLockRenewal(buildCtx, lockKey, token)
	defer func(ctx context.Context) {
		//nolint:errcheck // A lost or expired lock should not mask the build result.
		_ = s.locks.ReleaseDistributedLockWithToken(ctx, lockKey, token)
	}(context.WithoutCancel(ctx))
	defer stopRenewal()

	buildErrCh := make(chan error, 1)
	go func() {
		buildErrCh <- s.inner.Build(buildCtx, imageName)
	}()

	select {
	case err := <-buildErrCh:
		return err
	case err := <-renewalErrCh:
		cancel()
		<-buildErrCh
		return err
	}
}

func (s *imagePullLockedContainerSvc) startLockRenewal(ctx context.Context, lockKey, token string) (stop func(), errs <-chan error) {
	renewCtx, cancel := context.WithCancel(ctx)
	errCh := make(chan error, 1)
	interval := s.cfg.TTL / 3
	if interval < minImagePullLockRenewInterval {
		interval = minImagePullLockRenewInterval
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-renewCtx.Done():
				return
			case <-ticker.C:
				renewed, err := s.locks.ExtendDistributedLockWithToken(renewCtx, lockKey, token, s.cfg.TTL)
				if err != nil {
					errCh <- err
					return
				}
				if !renewed {
					errCh <- status.Error(codes.ResourceExhausted, "image pull lock ownership was lost")
					return
				}
			}
		}
	}()

	return cancel, errCh
}

func waitForImagePullLock(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func imagePullLockWaitError(parentCtx context.Context, err error) error {
	if parentCtx.Err() != nil {
		if errors.Is(parentCtx.Err(), context.DeadlineExceeded) {
			return status.Error(codes.DeadlineExceeded, parentCtx.Err().Error())
		}
		return status.Error(codes.Canceled, parentCtx.Err().Error())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.ResourceExhausted, "timed out waiting for image pull lock")
	}
	if errors.Is(err, context.Canceled) {
		return status.Error(codes.Canceled, err.Error())
	}
	return err
}

func imagePullLockKey(dockerHost, imageName string) string {
	return fmt.Sprintf("container:image-pull:%s:%s", sha256Hex(dockerHost), sha256Hex(imageName))
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (s *imagePullLockedContainerSvc) ImageExists(ctx context.Context, imageName string) (bool, error) {
	return s.inner.ImageExists(ctx, imageName)
}

func (s *imagePullLockedContainerSvc) DockerHost() string {
	return s.inner.DockerHost()
}

func (s *imagePullLockedContainerSvc) Logs(ctx context.Context, containerID string) (logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	return s.inner.Logs(ctx, containerID)
}

func (s *imagePullLockedContainerSvc) Remove(ctx context.Context, containerID string) error {
	return s.inner.Remove(ctx, containerID)
}

func (s *imagePullLockedContainerSvc) Terminate(ctx context.Context, containerID string) error {
	return s.inner.Terminate(ctx, containerID)
}
