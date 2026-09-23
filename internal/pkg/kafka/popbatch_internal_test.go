package kafka

import (
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// TestPopBatchSparseCapacity guards against a full-batch allocation for one record.
func TestPopBatchSparseCapacity(t *testing.T) {
	lane := &partitionLane{notify: make(chan struct{}, 1)}

	lane.enqueue([]*kgo.Record{{Topic: "t", Partition: 0}})

	got, err := lane.popBatch(t.Context(), 1000, time.Millisecond)
	if err != nil {
		t.Fatalf("popBatch() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("popBatch() len = %d, want 1", len(got))
	}
	if cap(got) != 1 {
		t.Fatalf("popBatch() cap = %d, want 1", cap(got))
	}
}
