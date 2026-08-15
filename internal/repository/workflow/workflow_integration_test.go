//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package workflow

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestMain(m *testing.M) {
	testkit.Run(m, testkit.WithRedis(), testkit.WithKafka())
}

// fakeContainerSvc tracks concurrent builds so lock serialization can be
// asserted against real Redis.
type fakeContainerSvc struct {
	mu            sync.Mutex
	builds        []string
	inBuild       map[string]int
	maxConcurrent map[string]int
	buildDelay    time.Duration
}

func (f *fakeContainerSvc) Build(ctx context.Context, imageName string) error {
	f.mu.Lock()
	f.inBuild[imageName]++
	if f.inBuild[imageName] > f.maxConcurrent[imageName] {
		f.maxConcurrent[imageName] = f.inBuild[imageName]
	}
	f.mu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(f.buildDelay):
	}

	f.mu.Lock()
	f.builds = append(f.builds, imageName)
	f.inBuild[imageName]--
	f.mu.Unlock()
	return nil
}

func (f *fakeContainerSvc) ResolveImageDigest(context.Context, string) (digest, imageName string, err error) {
	return "", "", status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeContainerSvc) ImageExists(context.Context, string) (bool, error) { return false, nil }
func (f *fakeContainerSvc) DockerHost() string                                { return "tcp://fake:2375" }

func (f *fakeContainerSvc) Logs(context.Context, string) (logs <-chan *jobsmodel.JobLog, errCh <-chan error, err error) {
	return nil, nil, status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeContainerSvc) Remove(context.Context, string) error {
	return status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeContainerSvc) Terminate(context.Context, string) error {
	return status.Error(codes.Unimplemented, "not implemented")
}

func TestIntegrationImagePullLockSerializesBuilds(t *testing.T) {
	ctx := context.Background()
	inner := &fakeContainerSvc{
		inBuild:       make(map[string]int),
		maxConcurrent: make(map[string]int),
		buildDelay:    200 * time.Millisecond,
	}

	svc := NewImagePullLockedContainerSvc(inner, testkit.Redis(t), ImagePullLockConfig{
		TTL:           5 * time.Second,
		WaitTimeout:   10 * time.Second,
		RetryInterval: 10 * time.Millisecond,
		LockScope:     "integration-test",
	})

	// Three concurrent pulls of the same image must be serialized by the
	// Redis-backed lock.
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := svc.Build(ctx, "alpine:3.22.2"); err != nil {
				t.Errorf("Build: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := inner.maxConcurrent["alpine:3.22.2"]; got != 1 {
		t.Fatalf("max concurrent builds = %d, want 1 (lock did not serialize)", got)
	}
	if got := len(inner.builds); got != 3 {
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
