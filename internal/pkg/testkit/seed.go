package testkit

import (
	"context"
	"testing"

	postgrespkg "github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

// SeedUser inserts a user row into the shared PostgreSQL instance and returns
// its id. Many repository schemas reference users(id), so integration tests
// need a real row before inserting dependent rows (workflows, jobs, ...).
func SeedUser(ctx context.Context, t *testing.T, pg *postgrespkg.Postgres, email string) string {
	t.Helper()

	var userID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO users (email, password)
		VALUES ($1, 'hash')
		RETURNING id
	`, email).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return userID
}

// SeedWorkflow inserts a workflow row owned by the given user with a default
// CONTAINER payload and COMPLETED build status, returning its id.
func SeedWorkflow(ctx context.Context, t *testing.T, pg *postgrespkg.Postgres, userID, name string) string {
	t.Helper()

	var workflowID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO workflows (user_id, name, payload, kind, build_status, interval, log_retention)
		VALUES ($1, $2, '{}', 'CONTAINER', 'COMPLETED', 1, TRUE)
		RETURNING id
	`, userID, name).Scan(&workflowID); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	return workflowID
}
