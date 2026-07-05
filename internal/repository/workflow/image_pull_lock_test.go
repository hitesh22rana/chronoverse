package workflow_test

import (
	"context"
	"sync"
	"testing"
	"time"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	workflowrepo "github.com/hitesh22rana/chronoverse/internal/repository/workflow"
)

func TestImagePullLockedContainerSvcBuildDelegatesToSharedEnsure(t *testing.T) {
	t.Parallel()

	inner := &fakeContainerSvc{dockerHost: "tcp://docker-a:2375"}
	locks := &fakeImagePullLockStore{acquireResults: []bool{true}}
	svc := workflowrepo.NewImagePullLockedContainerSvc(inner, locks, workflowrepo.ImagePullLockConfig{
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
	if locks.acquireCalls != 1 {
		t.Fatalf("AcquireDistributedLockWithToken calls = %d, want 1", locks.acquireCalls)
	}
}

type fakeImagePullLockStore struct {
	mu             sync.Mutex
	acquireResults []bool
	acquireCalls   int
}

func (s *fakeImagePullLockStore) AcquireDistributedLockWithToken(context.Context, string, time.Duration) (token string, acquired bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.acquireCalls++
	if len(s.acquireResults) == 0 {
		return "token", false, nil
	}

	acquired = s.acquireResults[0]
	s.acquireResults = s.acquireResults[1:]
	return "token", acquired, nil
}

func (*fakeImagePullLockStore) ExtendDistributedLockWithToken(context.Context, string, string, time.Duration) (bool, error) {
	return true, nil
}

func (*fakeImagePullLockStore) ReleaseDistributedLockWithToken(context.Context, string, string) error {
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

func (s *fakeContainerSvc) ResolveImageDigest(ctx context.Context, image string) (resolvedImageRef, resolvedImageDigest string, err error) {
	if err := s.Build(ctx, image); err != nil {
		return "", "", err
	}
	return image, image + "@sha256:test", nil
}

func (s *fakeContainerSvc) ImageExists(context.Context, string) (bool, error) {
	return s.imageExists, nil
}

func (s *fakeContainerSvc) DockerHost() string {
	return s.dockerHost
}

func (*fakeContainerSvc) Logs(context.Context, string) (logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	return nil, nil, nil
}

func (*fakeContainerSvc) Remove(context.Context, string) error {
	return nil
}

func (*fakeContainerSvc) Terminate(context.Context, string) error {
	return nil
}
