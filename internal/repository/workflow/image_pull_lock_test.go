package workflow

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
)

func TestImagePullLockedContainerSvcBuildCachedImageBypassesLock(t *testing.T) {
	t.Parallel()

	inner := &fakeContainerSvc{
		imageExists: true,
		dockerHost:  "tcp://docker-a:2375",
	}
	locks := &fakeImagePullLockStore{}
	svc := NewImagePullLockedContainerSvc(inner, locks, ImagePullLockConfig{
		TTL:           time.Minute,
		WaitTimeout:   time.Minute,
		RetryInterval: time.Millisecond,
	})

	if err := svc.Build(t.Context(), "alpine:3.22"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if inner.buildCalls != 0 {
		t.Fatalf("Build() inner calls = %d, want 0", inner.buildCalls)
	}
	if locks.acquireCalls != 0 {
		t.Fatalf("AcquireDistributedLockWithToken calls = %d, want 0", locks.acquireCalls)
	}
}

func TestImagePullLockedContainerSvcBuildAcquiresLockAndBuilds(t *testing.T) {
	t.Parallel()

	inner := &fakeContainerSvc{dockerHost: "tcp://docker-a:2375"}
	locks := &fakeImagePullLockStore{acquireResults: []bool{true}}
	svc := NewImagePullLockedContainerSvc(inner, locks, ImagePullLockConfig{
		TTL:           time.Minute,
		WaitTimeout:   time.Minute,
		RetryInterval: time.Millisecond,
	})

	if err := svc.Build(t.Context(), "alpine:3.22"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if inner.buildCalls != 1 {
		t.Fatalf("Build() inner calls = %d, want 1", inner.buildCalls)
	}
	if got, want := locks.keys[0], imagePullLockKey("tcp://docker-a:2375", "alpine:3.22"); got != want {
		t.Fatalf("lock key = %q, want %q", got, want)
	}
	if locks.releaseCalls != 1 {
		t.Fatalf("ReleaseDistributedLockWithToken calls = %d, want 1", locks.releaseCalls)
	}
}

func TestImagePullLockedContainerSvcBuildWaitsForHeldLock(t *testing.T) {
	t.Parallel()

	inner := &fakeContainerSvc{dockerHost: "tcp://docker-a:2375"}
	locks := &fakeImagePullLockStore{acquireResults: []bool{false, true}}
	svc := NewImagePullLockedContainerSvc(inner, locks, ImagePullLockConfig{
		TTL:           time.Minute,
		WaitTimeout:   time.Second,
		RetryInterval: time.Millisecond,
	})

	if err := svc.Build(t.Context(), "alpine:3.22"); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if locks.acquireCalls != 2 {
		t.Fatalf("AcquireDistributedLockWithToken calls = %d, want 2", locks.acquireCalls)
	}
	if inner.buildCalls != 1 {
		t.Fatalf("Build() inner calls = %d, want 1", inner.buildCalls)
	}
}

func TestImagePullLockedContainerSvcBuildWaitTimeoutIsRetryable(t *testing.T) {
	t.Parallel()

	inner := &fakeContainerSvc{dockerHost: "tcp://docker-a:2375"}
	locks := &fakeImagePullLockStore{acquireResults: []bool{false, false, false, false}}
	svc := NewImagePullLockedContainerSvc(inner, locks, ImagePullLockConfig{
		TTL:           time.Minute,
		WaitTimeout:   3 * time.Millisecond,
		RetryInterval: time.Millisecond,
	})

	err := svc.Build(t.Context(), "alpine:3.22")
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("Build() status code = %s, want %s: %v", status.Code(err), codes.ResourceExhausted, err)
	}
	if inner.buildCalls != 0 {
		t.Fatalf("Build() inner calls = %d, want 0", inner.buildCalls)
	}
}

func TestImagePullLockKeyIncludesDockerHost(t *testing.T) {
	t.Parallel()

	image := "alpine:3.22"
	first := imagePullLockKey("tcp://docker-a:2375", image)
	second := imagePullLockKey("tcp://docker-b:2375", image)
	if first == second {
		t.Fatalf("imagePullLockKey() must differ for different docker hosts")
	}
}

type fakeImagePullLockStore struct {
	mu             sync.Mutex
	acquireResults []bool
	acquireCalls   int
	releaseCalls   int
	keys           []string
}

func (s *fakeImagePullLockStore) AcquireDistributedLockWithToken(_ context.Context, key string, _ time.Duration) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.acquireCalls++
	s.keys = append(s.keys, key)
	if len(s.acquireResults) == 0 {
		return "token", false, nil
	}

	acquired := s.acquireResults[0]
	s.acquireResults = s.acquireResults[1:]
	return "token", acquired, nil
}

func (*fakeImagePullLockStore) ExtendDistributedLockWithToken(context.Context, string, string, time.Duration) (bool, error) {
	return true, nil
}

func (s *fakeImagePullLockStore) ReleaseDistributedLockWithToken(context.Context, string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseCalls++
	return nil
}

type fakeContainerSvc struct {
	imageExists bool
	dockerHost  string
	buildCalls  int
}

func (s *fakeContainerSvc) Build(context.Context, string) error {
	s.buildCalls++
	return nil
}

func (s *fakeContainerSvc) ImageExists(context.Context, string) (bool, error) {
	return s.imageExists, nil
}

func (s *fakeContainerSvc) DockerHost() string {
	return s.dockerHost
}

func (*fakeContainerSvc) Logs(context.Context, string) (<-chan *jobsmodel.JobLog, <-chan error, error) {
	return nil, nil, nil
}

func (*fakeContainerSvc) Remove(context.Context, string) error {
	return nil
}

func (*fakeContainerSvc) Terminate(context.Context, string) error {
	return nil
}
