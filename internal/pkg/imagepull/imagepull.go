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
	defaultLockTTL            = 10 * time.Minute
	defaultLockWaitTimeout    = 10 * time.Minute
	defaultLockRetryInterval  = 500 * time.Millisecond
	minLockTTL                = 3 * time.Millisecond
	defaultLockReleaseTimeout = 5 * time.Second
	defaultLockRenewTimeout   = 5 * time.Second
)

// Client can inspect and pull images from a single Docker daemon.
// Inspection and build must honor cancellation so protected work can be joined.
type Client interface {
	Build(ctx context.Context, imageName string) error
	ImageExists(ctx context.Context, imageName string) (bool, error)
	DockerHost() string
}

// LockStore coordinates image pulls between workers sharing a Docker daemon.
// Extension and release implementations must honor context deadlines so renewal
// shutdown and cleanup can complete within their I/O budgets.
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

// Validate checks explicitly configured coordination durations.
// Zero selects defaults; Redis TTL resolution is one millisecond.
func (c Config) Validate() error {
	if c.TTL != 0 && c.TTL < minLockTTL {
		return errors.New("TTL must be zero or at least 3ms")
	}
	if c.WaitTimeout < 0 {
		return errors.New("WAIT_TIMEOUT must be non-negative")
	}
	if c.RetryInterval < 0 {
		return errors.New("RETRY_INTERVAL must be non-negative")
	}
	return nil
}

// Ensure makes imageName available on client, serializing cold pulls per runtime scope.
func Ensure(ctx context.Context, client Client, locks LockStore, imageName string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid image-pull configuration: %v", err)
	}
	cfg = normalizeConfig(cfg)

	exists, err := client.ImageExists(ctx, imageName)
	if err != nil || exists {
		return err
	}

	lockScope := cfg.LockScope
	if lockScope == "" {
		lockScope = client.DockerHost()
	}
	key := lockKey(lockScope, imageName)
	waitCtx, cancel := context.WithTimeout(ctx, cfg.WaitTimeout)
	defer cancel()

	for {
		started := time.Now()
		token, acquired, err := locks.AcquireDistributedLockWithToken(waitCtx, key, cfg.TTL)
		if err != nil {
			return err
		}
		if acquired {
			return buildWithLock(ctx, client, locks, imageName, key, token, cfg, started.Add(cfg.TTL))
		}

		if err := waitForLock(waitCtx, cfg.RetryInterval); err != nil {
			return waitError(ctx, err)
		}
	}
}

func normalizeConfig(cfg Config) Config {
	if cfg.TTL == 0 {
		cfg.TTL = defaultLockTTL
	}
	cfg.TTL = cfg.TTL.Truncate(time.Millisecond)
	if cfg.WaitTimeout == 0 {
		cfg.WaitTimeout = defaultLockWaitTimeout
	}
	if cfg.RetryInterval == 0 {
		cfg.RetryInterval = defaultLockRetryInterval
	}
	return cfg
}

func buildWithLock(
	ctx context.Context,
	client Client,
	locks LockStore,
	imageName,
	lockKey,
	token string,
	cfg Config,
	expiresAt time.Time,
) (
	err error,
) {
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultLockReleaseTimeout)
		defer cancel()
		//nolint:errcheck // A lost or expired lock should not mask the build result.
		_ = locks.ReleaseDistributedLockWithToken(releaseCtx, lockKey, token)
	}()

	if !time.Now().Before(expiresAt.Add(-cfg.TTL / 3)) {
		return status.Error(codes.Unavailable, "image pull lock renewal budget exhausted after acquisition")
	}
	buildCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopRenewal := startLockRenewal(buildCtx, locks, lockKey, token, cfg.TTL, expiresAt, cancel)
	defer func() {
		if renewErr := stopRenewal(); renewErr != nil {
			err = imagePullResult(ctx, err, renewErr)
		}
	}()
	exists, err := client.ImageExists(buildCtx, imageName)
	if err != nil || exists {
		return err
	}
	if err := buildCtx.Err(); err != nil {
		return err
	}
	return client.Build(buildCtx, imageName)
}

func imagePullResult(caller context.Context, workErr, renewErr error) error {
	if workErr == nil || errors.Is(workErr, context.Canceled) || errors.Is(workErr, context.DeadlineExceeded) || status.Code(workErr) == codes.Canceled || status.Code(workErr) == codes.DeadlineExceeded {
		if err := caller.Err(); err != nil {
			return status.FromContextError(err).Err()
		}
		return renewErr
	}
	return workErr
}

func startLockRenewal(
	ctx context.Context,
	locks LockStore,
	lockKey,
	token string,
	ttl time.Duration,
	expiresAt time.Time,
	cancelWork context.CancelFunc,
) func() error {
	renewCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var renewalErr error

	go func() {
		defer func() {
			if renewalErr != nil {
				cancelWork()
			}
			close(done)
		}()
		interval := ttl / 3
		timer := time.NewTimer(time.Until(expiresAt.Add(-ttl + interval)))
		defer timer.Stop()

		for {
			select {
			case <-renewCtx.Done():
				return
			case <-timer.C:
				started := time.Now()
				deadline := started.Add(min(interval, defaultLockRenewTimeout))
				if latest := expiresAt.Add(-interval); latest.Before(deadline) {
					deadline = latest
				}
				if !deadline.After(started) {
					renewalErr = status.Error(codes.Unavailable, "image pull lock renewal budget exhausted")
					return
				}
				callCtx, stop := context.WithDeadline(renewCtx, deadline)
				renewed, err := locks.ExtendDistributedLockWithToken(callCtx, lockKey, token, ttl)
				stop()
				if renewCtx.Err() != nil {
					return
				}
				if err != nil {
					renewalErr = err
					return
				}
				if !renewed {
					renewalErr = status.Error(codes.ResourceExhausted, "image pull lock ownership was lost")
					return
				}
				// Request start is a conservative lower bound for Redis expiry.
				expiresAt = started.Add(ttl)
				timer.Reset(time.Until(started.Add(interval)))
			}
		}
	}()

	return func() error { cancel(); <-done; return renewalErr }
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

// lockKey returns the Redis key used to coordinate a runtime scope and image pair.
func lockKey(lockScope, imageName string) string {
	return fmt.Sprintf("container:image-pull:%s:%s", sha256Hex(lockScope), sha256Hex(imageName))
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
