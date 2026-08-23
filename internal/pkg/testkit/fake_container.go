package testkit

import (
	"context"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	containerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
)

// FakeContainerSvc is an in-memory container service double shared by
// integration tests. Build records per-image concurrency so lock
// serialization can be asserted against real Redis; every other method is an
// Unimplemented stub. It satisfies the union of the executor and workflow
// container service interfaces.
type FakeContainerSvc struct {
	mu            sync.Mutex
	builds        []string
	inBuild       map[string]int
	maxConcurrent map[string]int
	BuildDelay    time.Duration
}

// NewFakeContainerSvc creates the double with the given artificial build
// latency so overlapping builds can be detected reliably.
func NewFakeContainerSvc(buildDelay time.Duration) *FakeContainerSvc {
	return &FakeContainerSvc{
		inBuild:       make(map[string]int),
		maxConcurrent: make(map[string]int),
		BuildDelay:    buildDelay,
	}
}

// Build simulates an image build, recording per-image concurrency.
func (f *FakeContainerSvc) Build(ctx context.Context, imageName string) error {
	f.mu.Lock()
	f.inBuild[imageName]++
	if f.inBuild[imageName] > f.maxConcurrent[imageName] {
		f.maxConcurrent[imageName] = f.inBuild[imageName]
	}
	f.mu.Unlock()

	select {
	case <-ctx.Done():
		// A canceled build never completes; unwind the in-flight count so
		// later assertions on peak concurrency are not inflated.
		f.mu.Lock()
		f.inBuild[imageName]--
		f.mu.Unlock()
		return ctx.Err()
	case <-time.After(f.BuildDelay):
	}

	f.mu.Lock()
	f.builds = append(f.builds, imageName)
	f.inBuild[imageName]--
	f.mu.Unlock()
	return nil
}

// MaxConcurrentBuilds returns the peak number of simultaneously in-flight
// builds observed for the given image.
func (f *FakeContainerSvc) MaxConcurrentBuilds(imageName string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxConcurrent[imageName]
}

// CompletedBuilds returns every image whose build finished, in completion
// order.
func (f *FakeContainerSvc) CompletedBuilds() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.builds...)
}

// ResolveImageDigest is not implemented by the double.
func (*FakeContainerSvc) ResolveImageDigest(context.Context, string) (resolvedImageRef, resolvedImageDigest string, err error) {
	return "", "", status.Error(codes.Unimplemented, "not implemented")
}

// ImageExists always reports false so callers take the build path.
func (*FakeContainerSvc) ImageExists(context.Context, string) (bool, error) { return false, nil }

// DockerHost returns a fixed fake endpoint.
func (*FakeContainerSvc) DockerHost() string { return "tcp://fake:2375" }

// Execute is not implemented by the double.
func (*FakeContainerSvc) Execute(context.Context, time.Duration, string, []string, []string) (output string, logs <-chan *jobsmodel.JobLog, errCh <-chan error, err error) {
	return "", nil, nil, status.Error(codes.Unimplemented, "not implemented")
}

// Logs is not implemented by the double.
func (*FakeContainerSvc) Logs(context.Context, string) (logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	return nil, nil, status.Error(codes.Unimplemented, "not implemented")
}

// Inspect is not implemented by the double.
func (*FakeContainerSvc) Inspect(context.Context, string) (*containerpkg.State, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

// Remove is not implemented by the double.
func (*FakeContainerSvc) Remove(context.Context, string) error {
	return status.Error(codes.Unimplemented, "not implemented")
}

// Terminate is not implemented by the double.
func (*FakeContainerSvc) Terminate(context.Context, string) error {
	return status.Error(codes.Unimplemented, "not implemented")
}
