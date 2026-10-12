//nolint:testpackage // Covers preference update failure and no-op paths.
package users

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	otelcodes "go.opentelemetry.io/otel/codes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestIntegrationUpdateUserRejectsMalformedID(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo, recorder := newTracedRepository(t, issuingAuth(t), pg)

	id := insertUser(t, pg, "malformed-"+uuid.NewString()+"@chronoverse.test")
	preference, updatedAt := readPreference(t, pg, id)

	err := repo.UpdateUser(ctx, "not-a-uuid", "NONE")
	if status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "invalid user ID") {
		t.Fatalf("UpdateUser = %v", err)
	}
	assertUserErrorSpan(t, recorder, "UpdateUser", err)

	if got, gotAt := readPreference(t, pg, id); got != preference || !gotAt.Equal(updatedAt) {
		t.Fatalf("stored preference/updated_at = %q/%v, want %q/%v", got, gotAt, preference, updatedAt)
	}
}

func TestIntegrationUpdateUserMapsUpdateFaultToInternal(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo, recorder := newTracedRepository(t, issuingAuth(t), pg)

	email := "update-" + uuid.NewString() + "@chronoverse.test"
	id := insertUser(t, pg, email)
	_, updatedAt := readPreference(t, pg, id)

	remove := installRegistrationFault(t, pg, postgres.TableUsers, "UPDATE", "NEW.email", email, "RAISE EXCEPTION 'update fault';", false)
	err := repo.UpdateUser(ctx, id, "NONE")
	if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "failed to update user") {
		t.Fatalf("UpdateUser = %v", err)
	}
	assertUserErrorSpan(t, recorder, "UpdateUser", err)

	if got, gotAt := readPreference(t, pg, id); got != "ALERTS" || !gotAt.Equal(updatedAt) {
		t.Fatalf("stored preference/updated_at = %q/%v, want ALERTS/%v", got, gotAt, updatedAt)
	}
	remove()

	if err := repo.UpdateUser(ctx, id, "NONE"); err != nil {
		t.Fatalf("UpdateUser after removing the fault: %v", err)
	}
	if got, _ := readPreference(t, pg, id); got != "NONE" {
		t.Fatalf("stored preference = %q, want %q", got, "NONE")
	}
}

func TestIntegrationUpdateUserAcceptsUnchangedPreference(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo, recorder := newTracedRepository(t, issuingAuth(t), pg)

	id := insertUser(t, pg, "noop-"+uuid.NewString()+"@chronoverse.test")
	preference, updatedAt := readPreference(t, pg, id)

	if err := repo.UpdateUser(ctx, id, preference); err != nil {
		t.Fatalf("UpdateUser = %v", err)
	}
	if got, gotAt := readPreference(t, pg, id); got != preference || !gotAt.Equal(updatedAt) {
		t.Fatalf("stored preference/updated_at = %q/%v, want %q/%v", got, gotAt, preference, updatedAt)
	}
	spans := recorder.Ended()
	if len(spans) != 1 || spans[0].Name() != "Repository.UpdateUser" || spans[0].Status().Code != otelcodes.Unset {
		t.Fatalf("spans = %v, want one untraced-error update span", spans)
	}

	// The same no-op update for a missing account still reports not found.
	if err := repo.UpdateUser(ctx, "00000000-0000-0000-0000-000000000000", preference); status.Code(err) != codes.NotFound {
		t.Fatalf("UpdateUser(unknown id) = %v, want %v", err, codes.NotFound)
	}
}

func TestIntegrationUpdateUserFailsWhenStateLookupFails(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo, recorder := newTracedRepository(t, issuingAuth(t), pg)

	id := insertUser(t, pg, "lookup-"+uuid.NewString()+"@chronoverse.test")
	preference, updatedAt := readPreference(t, pg, id)

	remove := installUsersReadFault(t, pg, "RAISE EXCEPTION 'state lookup fault';")
	err := repo.UpdateUser(ctx, id, preference)
	if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "failed to check user preference state") {
		t.Fatalf("UpdateUser = %v", err)
	}
	assertUserErrorSpan(t, recorder, "UpdateUser", err)

	remove()
	if got, gotAt := readPreference(t, pg, id); got != preference || !gotAt.Equal(updatedAt) {
		t.Fatalf("stored preference/updated_at = %q/%v, want %q/%v", got, gotAt, preference, updatedAt)
	}
	if err := repo.UpdateUser(ctx, id, "NONE"); err != nil {
		t.Fatalf("UpdateUser after restoring the table: %v", err)
	}
	if got, _ := readPreference(t, pg, id); got != "NONE" {
		t.Fatalf("stored preference = %q, want %q", got, "NONE")
	}
}

func TestIntegrationUpdateUserInterruptsStateLookup(t *testing.T) {
	for _, code := range []codes.Code{codes.Canceled, codes.DeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			ctx := context.Background()
			pg := testkit.Postgres(t)
			repo, recorder := newTracedRepository(t, issuingAuth(t), pg)

			id := insertUser(t, pg, "interrupted-"+uuid.NewString()+"@chronoverse.test")
			preference, updatedAt := readPreference(t, pg, id)

			remove := installUsersReadFault(t, pg, "PERFORM pg_sleep(30); RETURN now();")
			commandCtx, cancel := context.WithCancel(ctx)
			if code == codes.DeadlineExceeded {
				commandCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
			}
			defer cancel()

			done := make(chan error, 1)
			go func() { done <- repo.UpdateUser(commandCtx, id, preference) }()
			waitForSleepingStateLookup(ctx, t, pg)
			if code == codes.Canceled {
				cancel()
			}
			var err error
			select {
			case err = <-done:
			case <-time.After(30 * time.Second):
				t.Fatal("UpdateUser did not return after the state lookup was interrupted")
			}
			if status.Code(err) != code {
				t.Fatalf("UpdateUser = %v, want %s", err, code)
			}
			assertUserErrorSpan(t, recorder, "UpdateUser", err)

			remove()
			if got, gotAt := readPreference(t, pg, id); got != preference || !gotAt.Equal(updatedAt) {
				t.Fatalf("stored preference/updated_at = %q/%v, want %q/%v", got, gotAt, preference, updatedAt)
			}
			if err := repo.UpdateUser(ctx, id, "NONE"); err != nil {
				t.Fatalf("UpdateUser after restoring the table: %v", err)
			}
		})
	}
}

// waitForSleepingStateLookup synchronizes interruption with the actual lookup.
func waitForSleepingStateLookup(ctx context.Context, t *testing.T, pg *postgres.Postgres) {
	t.Helper()

	query := `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
  WHERE datname = current_database() AND wait_event = 'PgSleep' AND query ILIKE '%SELECT EXISTS%')`
	testkit.Eventually(t, 10*time.Second, 10*time.Millisecond, func() bool {
		var sleeping bool
		if err := pg.QueryRow(ctx, query).Scan(&sleeping); err != nil {
			t.Errorf("probe sleeping state lookup: %v", err)
			return false
		}
		return sleeping
	})
}
