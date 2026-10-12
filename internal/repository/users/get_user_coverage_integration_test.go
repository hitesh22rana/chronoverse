//nolint:testpackage // Covers user lookup failure paths.
package users

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestIntegrationGetUserRejectsMalformedID(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo, recorder := newTracedRepository(t, issuingAuth(t), pg)

	id := insertUser(t, pg, "malformed-"+uuid.NewString()+"@chronoverse.test")
	preference, updatedAt := readPreference(t, pg, id)

	user, err := repo.GetUser(ctx, "not-a-uuid")
	if status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "invalid user ID") || user != nil {
		t.Fatalf("GetUser = %v, %v", user, err)
	}
	assertUserErrorSpan(t, recorder, "GetUser", err)

	// A rejected id must not have reached the stored account.
	if got, gotAt := readPreference(t, pg, id); got != preference || !gotAt.Equal(updatedAt) {
		t.Fatalf("stored preference/updated_at = %q/%v, want %q/%v", got, gotAt, preference, updatedAt)
	}
}

func TestIntegrationGetUserFailsWhenDatabaseUnavailable(t *testing.T) {
	ctx := context.Background()
	shared := testkit.Postgres(t)
	id := insertUser(t, shared, "offline-"+uuid.NewString()+"@chronoverse.test")

	repo, recorder := newTracedRepository(t, issuingAuth(t), closedPostgres(t))
	user, err := repo.GetUser(ctx, id)
	if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "failed to fetch user") || user != nil {
		t.Fatalf("GetUser = %v, %v", user, err)
	}
	assertUserErrorSpan(t, recorder, "GetUser", err)

	if _, err := newTestRepository(t).GetUser(ctx, id); err != nil {
		t.Fatalf("GetUser through the shared pool: %v", err)
	}
}
