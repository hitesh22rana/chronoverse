//nolint:testpackage // Shares the package's real PostgreSQL fixture.
package analyticsprocessor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	kafkapkg "github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func seedCleanupRows(t *testing.T, pg *postgres.Postgres, consumers []string, rows map[string]time.Time) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := pg.Exec(ctx, `DELETE FROM processed_events WHERE consumer = ANY($1::text[])`, consumers); err != nil {
			t.Errorf("remove cleanup fixtures: %v", err)
		}
	})
	for identity, createdAt := range rows {
		consumer, eventKey, _ := strings.Cut(identity, "/")
		if _, err := pg.Exec(t.Context(), `INSERT INTO processed_events (consumer, event_key, created_at) VALUES ($1, $2, $3)`, consumer, eventKey, createdAt); err != nil {
			t.Fatalf("seed processed event %s: %v", identity, err)
		}
	}
}

func assertCleanupRows(t *testing.T, pg *postgres.Postgres, consumers []string, want map[string]time.Time) {
	t.Helper()
	rows, err := pg.Query(t.Context(), `SELECT consumer, event_key, created_at FROM processed_events WHERE consumer = ANY($1::text[])`, consumers)
	if err != nil {
		t.Fatalf("read processed events: %v", err)
	}
	defer rows.Close()
	seen := make(map[string]bool)
	for rows.Next() {
		var consumer, eventKey string
		var createdAt time.Time
		if err := rows.Scan(&consumer, &eventKey, &createdAt); err != nil {
			t.Fatalf("scan processed event: %v", err)
		}
		identity := consumer + "/" + eventKey
		expected, ok := want[identity]
		if !ok || !createdAt.Equal(expected) {
			t.Errorf("unexpected or changed processed event %s at %s", identity, createdAt)
		}
		seen[identity] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate processed events: %v", err)
	}
	for identity := range want {
		if !seen[identity] {
			t.Errorf("processed event %s was removed", identity)
		}
	}
}

func TestIntegrationCleanupProcessedEventsRetentionAndBoundedBatches(t *testing.T) {
	pg := testkit.Postgres(t)
	repo := New(pg, testkit.Kafka(t), kafkapkg.NewPartitionLifecycle())
	consumer := "cleanup-" + uuid.NewString()
	otherConsumer := consumer + "-other"
	consumers := []string{consumer, otherConsumer}
	now := time.Now().UTC().Truncate(time.Microsecond)
	oldest := consumer + "/shared-key"
	second := consumer + "/second"
	third := consumer + "/third"
	want := map[string]time.Time{
		oldest:                        now.Add(-72 * time.Hour),
		second:                        now.Add(-48 * time.Hour),
		third:                         now.Add(-36 * time.Hour),
		consumer + "/recent":          now.Add(-time.Hour),
		consumer + "/future":          now.Add(time.Hour),
		otherConsumer + "/shared-key": now.Add(-time.Hour),
	}
	seedCleanupRows(t, pg, consumers, want)

	// Cleanup is table-wide and the package shares one database, so these seeded
	// rows must be the only expired processed_events rows for the count assertions.
	deleted, err := repo.CleanupProcessedEvents(t.Context(), 24*time.Hour, 1)
	if err != nil || deleted != 1 {
		t.Fatalf("first cleanup = %d, %v, want 1, nil", deleted, err)
	}
	delete(want, oldest)
	assertCleanupRows(t, pg, consumers, want)

	deleted, err = repo.CleanupProcessedEvents(t.Context(), 24*time.Hour, 10)
	if err != nil || deleted != 2 {
		t.Fatalf("drain cleanup = %d, %v, want 2, nil", deleted, err)
	}
	delete(want, second)
	delete(want, third)
	assertCleanupRows(t, pg, consumers, want)

	deleted, err = repo.CleanupProcessedEvents(t.Context(), 24*time.Hour, 10)
	if err != nil || deleted != 0 {
		t.Fatalf("empty cleanup = %d, %v, want 0, nil", deleted, err)
	}
	assertCleanupRows(t, pg, consumers, want)
}

// The context is canceled before the call, so this covers error mapping and the
// guarantee that an aborted cleanup never mutates rows, not mid-query cancellation.
func TestIntegrationCleanupProcessedEventsCanceledContextPreservesRows(t *testing.T) {
	pg := testkit.Postgres(t)
	repo := New(pg, testkit.Kafka(t), kafkapkg.NewPartitionLifecycle())
	consumer := "cleanup-cancel-" + uuid.NewString()
	consumers := []string{consumer}
	want := map[string]time.Time{consumer + "/expired": time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Microsecond)}
	seedCleanupRows(t, pg, consumers, want)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	deleted, err := repo.CleanupProcessedEvents(ctx, 24*time.Hour, 10)
	if status.Code(err) != codes.Internal || !strings.HasPrefix(status.Convert(err).Message(), "failed to cleanup processed events:") {
		t.Fatalf("canceled cleanup = %v, want Internal cleanup error", err)
	}
	if deleted != 0 {
		t.Fatalf("canceled cleanup deleted %d rows, want 0", deleted)
	}
	assertCleanupRows(t, pg, consumers, want)
}
