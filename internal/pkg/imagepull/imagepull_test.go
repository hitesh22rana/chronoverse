package imagepull_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/imagepull"
)

func TestEnsureCachedImageBypassesLock(t *testing.T) {
	t.Parallel()

	client := &fakeClient{
		imageExists: true,
		dockerHost:  "tcp://docker-a:2375",
	}
	locks := &fakeLockStore{}

	if err := imagepull.Ensure(t.Context(), client, locks, "alpine:3.22", imagepull.Config{
		TTL:           time.Minute,
		WaitTimeout:   time.Minute,
		RetryInterval: time.Millisecond,
	}); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if client.buildCalls != 0 {
		t.Fatalf("Build() calls = %d, want 0", client.buildCalls)
	}
	if locks.acquireCalls != 0 {
		t.Fatalf("AcquireDistributedLockWithToken calls = %d, want 0", locks.acquireCalls)
	}
}

func TestEnsureAcquiresLockAndBuilds(t *testing.T) {
	t.Parallel()

	client := &fakeClient{dockerHost: "tcp://docker-a:2375"}
	locks := &fakeLockStore{acquireResults: []bool{true}}

	if err := imagepull.Ensure(t.Context(), client, locks, "alpine:3.22", imagepull.Config{
		TTL:           time.Minute,
		WaitTimeout:   time.Minute,
		RetryInterval: time.Millisecond,
	}); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if client.buildCalls != 1 {
		t.Fatalf("Build() calls = %d, want 1", client.buildCalls)
	}
	if got, want := locks.keys[0], imagepull.LockKey("tcp://docker-a:2375", "alpine:3.22"); got != want {
		t.Fatalf("lock key = %q, want %q", got, want)
	}
	if locks.releaseCalls != 1 {
		t.Fatalf("ReleaseDistributedLockWithToken calls = %d, want 1", locks.releaseCalls)
	}
}

func TestEnsureUsesConfiguredLockScope(t *testing.T) {
	t.Parallel()

	client := &fakeClient{dockerHost: "tcp://docker-a:2375"}
	locks := &fakeLockStore{acquireResults: []bool{true}}

	if err := imagepull.Ensure(t.Context(), client, locks, "alpine:3.22", imagepull.Config{
		TTL:           time.Minute,
		WaitTimeout:   time.Minute,
		RetryInterval: time.Millisecond,
		LockScope:     "runtime-node-a",
	}); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if got, want := locks.keys[0], imagepull.LockKey("runtime-node-a", "alpine:3.22"); got != want {
		t.Fatalf("lock key = %q, want %q", got, want)
	}
}

func TestEnsureWaitsForHeldLock(t *testing.T) {
	t.Parallel()

	client := &fakeClient{dockerHost: "tcp://docker-a:2375"}
	locks := &fakeLockStore{acquireResults: []bool{false, true}}

	if err := imagepull.Ensure(t.Context(), client, locks, "alpine:3.22", imagepull.Config{
		TTL:           time.Minute,
		WaitTimeout:   time.Second,
		RetryInterval: time.Millisecond,
	}); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if locks.acquireCalls != 2 {
		t.Fatalf("AcquireDistributedLockWithToken calls = %d, want 2", locks.acquireCalls)
	}
	if client.buildCalls != 1 {
		t.Fatalf("Build() calls = %d, want 1", client.buildCalls)
	}
}

func TestEnsureWaitTimeoutIsRetryable(t *testing.T) {
	t.Parallel()

	client := &fakeClient{dockerHost: "tcp://docker-a:2375"}
	locks := &fakeLockStore{acquireResults: []bool{false, false, false, false}}

	err := imagepull.Ensure(t.Context(), client, locks, "alpine:3.22", imagepull.Config{
		TTL:           time.Minute,
		WaitTimeout:   3 * time.Millisecond,
		RetryInterval: time.Millisecond,
	})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("Ensure() status code = %s, want %s: %v", status.Code(err), codes.ResourceExhausted, err)
	}
	if client.buildCalls != 0 {
		t.Fatalf("Build() calls = %d, want 0", client.buildCalls)
	}
}

func TestLockKeyIncludesDockerHost(t *testing.T) {
	t.Parallel()

	image := "alpine:3.22"
	first := imagepull.LockKey("tcp://docker-a:2375", image)
	second := imagepull.LockKey("tcp://docker-b:2375", image)
	if first == second {
		t.Fatalf("LockKey() must differ for different docker hosts")
	}
}

type fakeLockStore struct {
	mu             sync.Mutex
	acquireResults []bool
	acquireCalls   int
	releaseCalls   int
	keys           []string
}

func (s *fakeLockStore) AcquireDistributedLockWithToken(_ context.Context, key string, _ time.Duration) (token string, acquired bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.acquireCalls++
	s.keys = append(s.keys, key)
	if len(s.acquireResults) == 0 {
		return "token", false, nil
	}

	acquired = s.acquireResults[0]
	s.acquireResults = s.acquireResults[1:]
	return "token", acquired, nil
}

func (*fakeLockStore) ExtendDistributedLockWithToken(context.Context, string, string, time.Duration) (bool, error) {
	return true, nil
}

func (s *fakeLockStore) ReleaseDistributedLockWithToken(context.Context, string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseCalls++
	return nil
}

type fakeClient struct {
	imageExists bool
	dockerHost  string
	buildCalls  int
}

func (s *fakeClient) Build(context.Context, string) error {
	s.buildCalls++
	return nil
}

func (s *fakeClient) ImageExists(context.Context, string) (bool, error) {
	return s.imageExists, nil
}

func (s *fakeClient) DockerHost() string {
	return s.dockerHost
}
