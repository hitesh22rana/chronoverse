package imagepull

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultLockTTL           = 10 * time.Minute
	defaultLockWaitTimeout   = 10 * time.Minute
	defaultLockRetryInterval = 500 * time.Millisecond
	minLockRenewInterval     = time.Second
)

// Client can inspect and pull images from a single Docker daemon.
type Client interface {
	Build(ctx context.Context, imageName string) error
	ImageExists(ctx context.Context, imageName string) (bool, error)
	DockerHost() string
}

// LockStore coordinates image pulls between workers sharing a Docker daemon.
type LockStore interface {
	AcquireDistributedLockWithToken(ctx context.Context, key string, expiration time.Duration) (string, bool, error)
	ExtendDistributedLockWithToken(ctx context.Context, key, token string, expiration time.Duration) (bool, error)
	ReleaseDistributedLockWithToken(ctx context.Context, key, token string) error
}

// Config configures Docker image pull coordination.
type Config struct {
	TTL           time.Duration
	WaitTimeout   time.Duration
	RetryInterval time.Duration
	LockScope     string
}

// Ensure makes imageName available on client, serializing cold pulls per runtime scope.
func Ensure(ctx context.Context, client Client, locks LockStore, imageName string, cfg Config) error {
	cfg = normalizeConfig(cfg)

	exists, err := client.ImageExists(ctx, imageName)
	if err != nil || exists {
		return err
	}

	lockScope := cfg.LockScope
	if lockScope == "" {
		lockScope = client.DockerHost()
	}
	lockKey := LockKey(lockScope, imageName)
	waitCtx, cancel := context.WithTimeout(ctx, cfg.WaitTimeout)
	defer cancel()

	for {
		token, acquired, err := locks.AcquireDistributedLockWithToken(waitCtx, lockKey, cfg.TTL)
		if err != nil {
			return err
		}
		if acquired {
			return buildWithLock(ctx, client, locks, imageName, lockKey, token, cfg)
		}

		if err := waitForLock(waitCtx, cfg.RetryInterval); err != nil {
			return waitError(ctx, err)
		}
	}
}

func normalizeConfig(cfg Config) Config {
	if cfg.TTL <= 0 {
		cfg.TTL = defaultLockTTL
	}
	if cfg.WaitTimeout <= 0 {
		cfg.WaitTimeout = defaultLockWaitTimeout
	}
	if cfg.RetryInterval <= 0 {
		cfg.RetryInterval = defaultLockRetryInterval
	}
	return cfg
}

func buildWithLock(ctx context.Context, client Client, locks LockStore, imageName, lockKey, token string, cfg Config) error {
	buildCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stopRenewal, renewalErrCh := startLockRenewal(buildCtx, locks, lockKey, token, cfg.TTL)
	defer func(ctx context.Context) {
		//nolint:errcheck // A lost or expired lock should not mask the build result.
		_ = locks.ReleaseDistributedLockWithToken(ctx, lockKey, token)
	}(context.WithoutCancel(ctx))
	defer stopRenewal()

	buildErrCh := make(chan error, 1)
	go func() {
		buildErrCh <- client.Build(buildCtx, imageName)
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

func startLockRenewal(ctx context.Context, locks LockStore, lockKey, token string, ttl time.Duration) (stop func(), errs <-chan error) {
	renewCtx, cancel := context.WithCancel(ctx)
	errCh := make(chan error, 1)
	interval := ttl / 3
	if interval < minLockRenewInterval {
		interval = minLockRenewInterval
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-renewCtx.Done():
				return
			case <-ticker.C:
				renewed, err := locks.ExtendDistributedLockWithToken(renewCtx, lockKey, token, ttl)
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

func waitForLock(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func waitError(parentCtx context.Context, err error) error {
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

// LockKey returns the Redis key used to coordinate a runtime scope and image pair.
func LockKey(lockScope, imageName string) string {
	return fmt.Sprintf("container:image-pull:%s:%s", sha256Hex(lockScope), sha256Hex(imageName))
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
