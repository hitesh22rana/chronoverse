//nolint:testpackage // Exercises the shared image-pull lease against real Redis.
package workflow

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hitesh22rana/chronoverse/internal/pkg/imagepull"
	redispkg "github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

type shortLeaseStore struct {
	*redispkg.Store
	key string
}

func (s *shortLeaseStore) AcquireDistributedLockWithToken(
	ctx context.Context,
	key string,
	ttl time.Duration,
) (
	token string,
	acquired bool,
	err error,
) {
	s.key = key
	return s.Store.AcquireDistributedLockWithToken(ctx, key, ttl)
}

type shortLeaseClient struct {
	store *shortLeaseStore
	host  string
}

func (c shortLeaseClient) DockerHost() string                                { return c.host }
func (c shortLeaseClient) ImageExists(context.Context, string) (bool, error) { return false, nil }
func (c shortLeaseClient) Build(ctx context.Context, _ string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2700 * time.Millisecond):
	}
	token, acquired, err := c.store.Store.AcquireDistributedLockWithToken(ctx, c.store.key, time.Second)
	if err != nil {
		return err
	}
	if acquired {
		if err := c.store.Store.ReleaseDistributedLockWithToken(ctx, c.store.key, token); err != nil {
			return err
		}
		return fmt.Errorf("second owner acquired image-pull lock while first build was active")
	}
	return nil
}

func TestIntegrationImagePullShortLeaseMaintainsOwnership(t *testing.T) {
	store := &shortLeaseStore{Store: testkit.Redis(t)}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	err := imagepull.Ensure(ctx, shortLeaseClient{store: store, host: "tcp://" + uuid.NewString()}, store, "image", imagepull.Config{TTL: 900 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	token, acquired, err := store.Store.AcquireDistributedLockWithToken(ctx, store.key, time.Second)
	if err != nil || !acquired {
		t.Fatalf("completed pull retained its lock: acquired=%v err=%v", acquired, err)
	}
	if err := store.Store.ReleaseDistributedLockWithToken(ctx, store.key, token); err != nil {
		t.Fatal(err)
	}
}
