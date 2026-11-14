//nolint:testpackage // Exercises lifecycle coordination against real Redis with a short lease.
package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestIntegrationLifecycleLockRenewsUntilWorkFinishes(t *testing.T) {
	store := testkit.Redis(t)
	repo := &Repository{rdb: store}
	key := "workflow:BUILD:" + uuid.NewString()
	const ttl = time.Second
	err := repo.withLifecycleLock(t.Context(), key, ttl, func(ctx context.Context) error {
		timer := time.NewTimer(3 * ttl)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			t.Fatalf("work lost ownership: %v", context.Cause(ctx))
		case <-timer.C:
		}
		token, acquired, err := store.AcquireDistributedLockWithToken(ctx, key, ttl)
		if err != nil {
			t.Fatal(err)
		}
		if acquired {
			if err := store.ReleaseDistributedLockWithToken(ctx, key, token); err != nil {
				t.Error(err)
			}
			t.Fatal("active lifecycle lock expired while work was running")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	token, acquired, err := store.AcquireDistributedLockWithToken(t.Context(), key, ttl)
	if err != nil || !acquired {
		t.Fatalf("finished operation retained lock: acquired=%v err=%v", acquired, err)
	}
	if err := store.ReleaseDistributedLockWithToken(t.Context(), key, token); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationLifecycleLockLossCancelsWorkAndPreservesSuccessor(t *testing.T) {
	store := testkit.Redis(t)
	client := goredis.NewClient(&goredis.Options{Addr: testkit.RedisAddr(t)})
	defer client.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	key := "workflow:BUILD:" + uuid.NewString()
	defer client.Del(t.Context(), key)
	err := (&Repository{rdb: store}).withLifecycleLock(ctx, key, 300*time.Millisecond, func(ctx context.Context) error {
		if err := client.Set(ctx, key, "successor", time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
		<-ctx.Done()
		return nil
	})
	if status.Code(err) != codes.Aborted {
		t.Fatalf("lost owner result = %v", err)
	}
	if token, err := client.Get(t.Context(), key).Result(); err != nil || token != "successor" {
		t.Fatalf("successor lock = %q, %v", token, err)
	}
}
