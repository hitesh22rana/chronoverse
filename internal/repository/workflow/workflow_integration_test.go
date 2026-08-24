//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package workflow

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestMain(m *testing.M) {
	testkit.Run(m, testkit.WithRedis(), testkit.WithKafka())
}

func TestIntegrationImagePullLockSerializesBuilds(t *testing.T) {
	ctx := context.Background()
	inner := testkit.NewFakeContainerSvc(200 * time.Millisecond)

	svc := NewImagePullLockedContainerSvc(inner, testkit.Redis(t), ImagePullLockConfig{
		TTL:           5 * time.Second,
		WaitTimeout:   10 * time.Second,
		RetryInterval: 10 * time.Millisecond,
		LockScope:     "integration-test",
	})

	// Three concurrent pulls of the same image must be serialized by the
	// Redis-backed lock.
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := svc.Build(ctx, "alpine:3.22.2"); err != nil {
				t.Errorf("Build: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := inner.MaxConcurrentBuilds("alpine:3.22.2"); got != 1 {
		t.Fatalf("max concurrent builds = %d, want 1 (lock did not serialize)", got)
	}
	if got := len(inner.CompletedBuilds()); got != 3 {
		t.Fatalf("builds = %d, want 3", got)
	}

	// The lock is released after the build; the next pull proceeds immediately.
	before := time.Now()
	if err := svc.Build(ctx, "alpine:3.22.2"); err != nil {
		t.Fatalf("Build after release: %v", err)
	}
	if elapsed := time.Since(before); elapsed > 2*time.Second {
		t.Fatalf("Build after release took %v, want < 2s (lock not released)", elapsed)
	}
}
