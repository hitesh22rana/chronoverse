//nolint:testpackage // Gate invariants require exercising the unexported registry directly.
package executor

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

func TestHandoffGetOrReserveIsAtomic(t *testing.T) {
	gate := newHandoffRegistry(1)
	request := &jobspb.ClaimJobRequest{Id: "job", CommandId: "claim"}

	var owners atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			_, owner, err := gate.getOrReserve("claim", request)
			if err != nil {
				t.Errorf("getOrReserve() error = %v", err)
				return
			}
			if owner {
				owners.Add(1)
			}
		})
	}
	wg.Wait()

	if owners.Load() != 1 {
		t.Fatalf("owners = %d, want 1", owners.Load())
	}
	if gate.size() != 1 {
		t.Fatalf("tracked permits = %d, want 1", gate.size())
	}
}

func TestHandoffCapacityAndOwnerResolution(t *testing.T) {
	gate := newHandoffRegistry(1)
	owner, reserved, err := gate.getOrReserve("first", &jobspb.ClaimJobRequest{Id: "job-1"})
	if err != nil || !reserved {
		t.Fatalf("first reservation = (%v, %v), want owner", reserved, err)
	}
	if _, _, err = gate.getOrReserve("second", &jobspb.ClaimJobRequest{Id: "job-2"}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("capacity error = %v, want ResourceExhausted", err)
	}

	waiter, waiterOwner, err := gate.getOrReserve("first", &jobspb.ClaimJobRequest{Id: "job-1"})
	if err != nil || waiterOwner || waiter != owner {
		t.Fatalf("duplicate reservation = (%v, %v, %v)", waiter, waiterOwner, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err = gate.wait(canceled, waiter); err == nil {
		t.Fatal("canceled waiter unexpectedly succeeded")
	}
	if gate.size() != 1 {
		t.Fatal("canceled waiter released the owner's permit")
	}

	gate.resolveRemoved(owner, status.Error(codes.Unavailable, "claim failed"))
	if gate.size() != 0 {
		t.Fatal("owner resolution did not release its permit")
	}
}

func TestHandoffAmbiguousEntryRequiresReconciliation(t *testing.T) {
	gate := newHandoffRegistry(1)
	entry, owner, err := gate.getOrReserve("claim", &jobspb.ClaimJobRequest{Id: "job"})
	if err != nil || !owner {
		t.Fatalf("getOrReserve() = (%v, %v), want owner", owner, err)
	}
	claim := &jobspb.ClaimJobResponse{Claimed: true, Id: "job", LeaseToken: "secret"}
	if !gate.activate(entry, claim) {
		t.Fatal("activate() = false")
	}
	gate.markAwaiting("claim")
	if len(gate.awaiting()) != 1 {
		t.Fatal("ambiguous handoff was not retained")
	}
	gate.consume("job", "different")
	if gate.size() != 1 {
		t.Fatal("different lease consumed the gate")
	}
	gate.removeAwaiting(entry)
	if gate.size() != 0 {
		t.Fatal("database reconciliation did not release the permit")
	}
}
