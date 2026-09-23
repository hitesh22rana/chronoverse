package kafka

import (
	"context"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

func newTestLane(ctx context.Context) (*partitionLane, context.CancelFunc) {
	laneCtx, cancel := context.WithCancel(ctx)
	return &partitionLane{
		key:    partitionKey{topic: "t", partition: 0},
		done:   laneCtx.Done(),
		cancel: cancel,
		notify: make(chan struct{}, 1),
		queue:  make([]*kgo.Record, 0, 4),
	}, cancel
}

// TestPopBatchSparseCapacity guards against preallocating a full batch for
// sparsely active partitions: one queued record with a large maxRecords must
// not allocate room for the whole batch.
func TestPopBatchSparseCapacity(t *testing.T) {
	lane, cancel := newTestLane(t.Context())
	defer cancel()

	lane.enqueue([]*kgo.Record{{Topic: "t", Partition: 0}})

	got, err := lane.popBatch(t.Context(), 1000, time.Millisecond)
	if err != nil {
		t.Fatalf("popBatch() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("popBatch() len = %d, want 1", len(got))
	}
	if cap(got) != 1 {
		t.Fatalf("popBatch() cap = %d, want 1 (no full-batch prealloc for sparse partitions)", cap(got))
	}
}

// TestPopBatchDenseExactCapacity guards the dense path: a fully queued batch
// keeps its exact preallocation (no waste, no regrowth).
func TestPopBatchDenseExactCapacity(t *testing.T) {
	lane, cancel := newTestLane(t.Context())
	defer cancel()

	recs := make([]*kgo.Record, 100)
	for i := range recs {
		recs[i] = &kgo.Record{Topic: "t", Partition: 0, Offset: int64(i)}
	}
	lane.enqueue(recs)

	got, err := lane.popBatch(t.Context(), 100, 0)
	if err != nil {
		t.Fatalf("popBatch() error = %v", err)
	}
	if len(got) != 100 {
		t.Fatalf("popBatch() len = %d, want 100", len(got))
	}
	if cap(got) != 100 {
		t.Fatalf("popBatch() cap = %d, want 100 (exact presize, no regrowth)", cap(got))
	}
}
