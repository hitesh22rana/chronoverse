package otel_test

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	otelpkg "github.com/hitesh22rana/chronoverse/internal/pkg/otel"
)

func TestInitResourceHostnamePermissionFailure(t *testing.T) {
	type result struct {
		setup           error
		threadID        int
		resourcePresent bool
		err             error
	}
	results := make(chan result, 1)
	workerFinished := make(chan struct{})
	dispatcherFinished := make(chan struct{})
	runFilteredWorker := func() {
		defer close(workerFinished)
		// Linux Hostname first calls uname, then falls back to opening /proc.
		// Deny both operations on this thread only. Exiting the locked goroutine
		// destroys the filtered thread, leaving the test/coverage runtime unaffected.
		threadID := unix.Gettid()
		filters := []unix.SockFilter{
			{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0},
			{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: uint32(unix.SYS_UNAME), Jt: 2},
			{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: uint32(unix.SYS_OPENAT), Jt: 1},
			{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
			{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)},
		}
		program := unix.SockFprog{Len: 5, Filter: &filters[0]}
		if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
			results <- result{setup: err, threadID: threadID}
			return
		}
		// #nosec G103 -- Kernel seccomp requires a pointer to this live, thread-local BPF program.
		if err := unix.Prctl(unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER, uintptr(unsafe.Pointer(&program)), 0, 0); err != nil {
			results <- result{setup: err, threadID: threadID}
			return
		}
		runtime.KeepAlive(filters)
		res, err := otelpkg.InitResource(t.Context(), "service", "version")
		results <- result{resourcePresent: res != nil, err: err, threadID: threadID}
	}
	go func() {
		defer close(dispatcherFinished)
		runtime.LockOSThread()
		if unix.Gettid() == os.Getpid() {
			// Go parks its main OS thread instead of destroying it. Keep that
			// thread unfiltered and occupied until a different worker finishes.
			go func() {
				runtime.LockOSThread()
				runFilteredWorker()
			}()
			<-workerFinished
			runtime.UnlockOSThread()
			return
		}
		runFilteredWorker()
	}()
	got := <-results
	<-workerFinished
	<-dispatcherFinished
	waitForWorkerThreadExit(t, got.threadID)
	if _, err := os.Hostname(); err != nil {
		t.Fatalf("hostname restriction escaped worker thread: %v", err)
	}
	file, err := os.CreateTemp(t.TempDir(), "unfiltered-*")
	if err != nil {
		t.Fatalf("file restriction escaped worker thread: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if got.setup != nil {
		t.Skipf("kernel disallows thread-local seccomp: %v", got.setup)
	}
	if got.resourcePresent || status.Code(got.err) != codes.Internal || !strings.Contains(got.err.Error(), "failed to get hostname") || !strings.Contains(got.err.Error(), "operation not permitted") {
		t.Fatalf("hostname OS failure: resource=%v error=%v", got.resourcePresent, got.err)
	}
}

func waitForWorkerThreadExit(t *testing.T, threadID int) {
	t.Helper()
	// The deferred completion signal precedes runtime thread teardown. Wait for
	// Linux to confirm the locked worker task has disappeared before checking
	// that unrelated work remains unrestricted.
	threadDeadline := time.Now().Add(time.Second)
	for {
		err := unix.Tgkill(os.Getpid(), threadID, 0)
		if errors.Is(err, unix.ESRCH) {
			break
		}
		if err != nil {
			t.Fatalf("check worker thread exit: %v", err)
		}
		if time.Now().After(threadDeadline) {
			t.Fatal("filtered worker thread did not exit")
		}
		time.Sleep(time.Millisecond)
	}
}
