//nolint:testpackage // Exercises cleanup without opening database connections.
package analyticsprocessor

import (
	"testing"
	"time"
)

func TestCleanupProcessedEventsInvalidBoundsAreNoOp(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		retention time.Duration
		batchSize int
	}{
		{"zero retention", 0, 10},
		{"negative retention", -time.Hour, 10},
		{"zero batch", time.Hour, 0},
		{"negative batch", time.Hour, -1},
		{"both zero", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// A no-op must not access the database.
			repo := &Repository{}
			deleted, err := repo.CleanupProcessedEvents(t.Context(), tc.retention, tc.batchSize)
			if err != nil || deleted != 0 {
				t.Fatalf("CleanupProcessedEvents() = %d, %v, want 0, nil", deleted, err)
			}
		})
	}
}
