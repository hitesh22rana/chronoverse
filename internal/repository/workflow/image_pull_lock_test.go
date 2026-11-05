package workflow_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/imagepull"
	workflowrepo "github.com/hitesh22rana/chronoverse/internal/repository/workflow"
)

func TestImagePullLockedContainerSvcBuildDelegatesToSharedEnsure(t *testing.T) {
	t.Parallel()

	inner := &fakeContainerSvc{dockerHost: "tcp://docker-a:2375"}
	locks := &fakeImagePullLockStore{acquireResults: []bool{true}}
	svc := workflowrepo.NewImagePullLockedContainerSvc(inner, locks, imagepull.Config{
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

type digestContextKey struct{}

type digestContractContainer struct {
	workflowrepo.ContainerSvc
	buildErr, resolveErr error
	built, resolved      []string
	seenValue            any
}

func (s *digestContractContainer) Build(ctx context.Context, image string) error {
	s.seenValue = ctx.Value(digestContextKey{})
	s.built = append(s.built, image)
	return s.buildErr
}

func (*digestContractContainer) ImageExists(context.Context, string) (bool, error) { return false, nil }

func (*digestContractContainer) DockerHost() string { return "tcp://digest-contract:2376" }

func (s *digestContractContainer) ResolveImageDigest(ctx context.Context, image string) (resolvedImageRef, resolvedImageDigest string, err error) {
	s.seenValue = ctx.Value(digestContextKey{})
	s.resolved = append(s.resolved, image)
	if s.resolveErr != nil {
		return "", "", s.resolveErr
	}
	return image, "image@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil
}

func TestImagePullLockedDigestResolutionPreservesIdentityAndFailure(t *testing.T) {
	t.Parallel()
	failure := errors.New("registry unavailable")
	for _, tc := range []struct {
		name                 string
		buildErr, resolveErr error
	}{
		{name: "success"}, {name: "pull failure", buildErr: failure}, {name: "inspect failure", resolveErr: failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			inner := &digestContractContainer{buildErr: tc.buildErr, resolveErr: tc.resolveErr}
			svc := workflowrepo.NewImagePullLockedContainerSvc(inner, &fakeImagePullLockStore{acquireResults: []bool{true}}, imagepull.Config{TTL: time.Minute})
			ctx := context.WithValue(t.Context(), digestContextKey{}, "caller-value")
			ref, digest, err := svc.ResolveImageDigest(ctx, "image:tag")
			wantErr := tc.resolveErr
			if tc.buildErr != nil {
				wantErr = tc.buildErr
			}
			if !errors.Is(err, wantErr) || len(inner.built) != 1 || inner.built[0] != "image:tag" || inner.seenValue != "caller-value" {
				t.Fatalf("resolution ref=%q digest=%q err=%v build=%v", ref, digest, err, inner.built)
			}
			if tc.buildErr != nil {
				assertDigestPair(t, ref, digest, "image:tag", "")
				if len(inner.resolved) != 0 {
					t.Fatalf("pull failure inspected or lost input ref: ref=%q digest=%q calls=%v", ref, digest, inner.resolved)
				}
				return
			}
			if len(inner.resolved) != 1 || inner.resolved[0] != "image:tag" {
				t.Fatalf("inspection inputs=%v", inner.resolved)
			}
			if tc.resolveErr == nil {
				assertDigestPair(t, ref, digest, "image:tag", "image@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
			}
		})
	}
}

func assertDigestPair(t *testing.T, ref, digest, wantRef, wantDigest string) {
	t.Helper()
	if ref != wantRef || digest != wantDigest {
		t.Fatalf("resolved identity=%q/%q want %q/%q", ref, digest, wantRef, wantDigest)
	}
}
