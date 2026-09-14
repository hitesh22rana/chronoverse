package imagepull_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/imagepull"
)

type fakeGuardClient struct {
	fakeClient
	storageCalls int
	storageErr   error
}

func (s *fakeGuardClient) CheckImageStorage(context.Context) error {
	s.storageCalls++
	return s.storageErr
}

func gateTestConfig() imagepull.Config {
	return imagepull.Config{
		TTL:               time.Minute,
		WaitTimeout:       time.Minute,
		RetryInterval:     time.Millisecond,
		LockScope:         "runtime-node-a",
		StorageLimitBytes: 1 << 30,
	}
}

func TestEnsureStorageGateChecksAndSerializes(t *testing.T) {
	t.Parallel()

	client := &fakeGuardClient{fakeClient: fakeClient{dockerHost: "tcp://docker-a:2375"}}
	locks := &fakeLockStore{acquireResults: []bool{true, true}}

	if err := imagepull.Ensure(t.Context(), client, locks, "alpine:3.22", gateTestConfig()); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if client.storageCalls != 1 {
		t.Fatalf("CheckImageStorage() calls = %d, want 1", client.storageCalls)
	}
	if client.buildCalls != 1 {
		t.Fatalf("Build() calls = %d, want 1", client.buildCalls)
	}
	// Gate first: the reserve check must hold the per-daemon gate before the
	// per-image lock, so concurrent pulls cannot interleave check and pull.
	if len(locks.keys) != 2 {
		t.Fatalf("lock keys = %v, want gate + image lock", locks.keys)
	}
	if got, want := locks.keys[1], imagepull.LockKey("runtime-node-a", "alpine:3.22"); got != want {
		t.Fatalf("image lock key = %q, want %q", got, want)
	}
	if locks.releaseCalls != 2 {
		t.Fatalf("ReleaseDistributedLockWithToken calls = %d, want 2 (gate + image)", locks.releaseCalls)
	}
}

func TestEnsureStorageGateRejectsUnguardedClient(t *testing.T) {
	t.Parallel()

	client := &fakeClient{dockerHost: "tcp://docker-a:2375"}
	locks := &fakeLockStore{acquireResults: []bool{true}}

	err := imagepull.Ensure(t.Context(), client, locks, "alpine:3.22", gateTestConfig())
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("Ensure() code = %s, want %s: %v", status.Code(err), codes.FailedPrecondition, err)
	}
	if client.buildCalls != 0 {
		t.Fatalf("Build() calls = %d, want 0", client.buildCalls)
	}
}

func TestEnsureStorageGateRefusalFailsPull(t *testing.T) {
	t.Parallel()

	client := &fakeGuardClient{
		fakeClient: fakeClient{dockerHost: "tcp://docker-a:2375"},
		storageErr: status.Error(codes.ResourceExhausted, "daemon image storage over budget"),
	}
	locks := &fakeLockStore{acquireResults: []bool{true}}

	err := imagepull.Ensure(t.Context(), client, locks, "alpine:3.22", gateTestConfig())
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("Ensure() code = %s, want %s: %v", status.Code(err), codes.ResourceExhausted, err)
	}
	if client.buildCalls != 0 {
		t.Fatalf("Build() calls = %d, want 0", client.buildCalls)
	}
	if locks.releaseCalls != 1 {
		t.Fatalf("ReleaseDistributedLockWithToken calls = %d, want 1 (gate released)", locks.releaseCalls)
	}
}

func TestEnsureStorageGateWaitIsBounded(t *testing.T) {
	t.Parallel()

	client := &fakeGuardClient{fakeClient: fakeClient{dockerHost: "tcp://docker-a:2375"}}
	locks := &fakeLockStore{} // never grants the gate

	cfg := gateTestConfig()
	cfg.WaitTimeout = 30 * time.Millisecond
	cfg.RetryInterval = 5 * time.Millisecond

	err := imagepull.Ensure(t.Context(), client, locks, "alpine:3.22", cfg)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("Ensure() code = %s, want %s: %v", status.Code(err), codes.ResourceExhausted, err)
	}
	if client.storageCalls != 0 {
		t.Fatalf("CheckImageStorage() calls = %d, want 0 (gate never held)", client.storageCalls)
	}
}

func TestEnsureNoGateWhenUnlimited(t *testing.T) {
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
	if len(locks.keys) != 1 {
		t.Fatalf("lock keys = %v, want image lock only", locks.keys)
	}
}
