//nolint:testpackage // Exercises notification command transactions and legacy rows.
package notifications

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestIntegrationNotificationLegacyAdoptionContracts(t *testing.T) {
	for _, payload := range []string{`{"message":"same"}`, `{"message":"different"}`, `[]`} {
		t.Run(payload, func(t *testing.T) {
			repo := newTestRepository(t, "ALL", 20)
			user := seedNotificationContractUser(t, repo)
			key := uuid.NewString()
			var legacy string
			query := `INSERT INTO notifications(user_id,kind,payload,idempotency_key) VALUES($1,'WEB_INFO',$2,$3) RETURNING id`
			if err := repo.pg.QueryRow(t.Context(), query, user, payload, "  "+key+"  ").Scan(&legacy); err != nil {
				t.Fatal(err)
			}
			id, err := repo.CreateNotification(t.Context(), user, "WEB_INFO", `{"message":"same"}`, key)
			want := codes.OK
			switch payload {
			case `[]`:
				want = codes.Internal
			case `{"message":"different"}`:
				want = codes.AlreadyExists
			}
			if status.Code(err) != want {
				t.Fatalf("id=%q error=%v want %s", id, err, want)
			}
			if want == codes.OK {
				if id != legacy {
					t.Fatalf("adopted %q want %q", id, legacy)
				}
				replay, replayErr := repo.CreateNotification(t.Context(), user, "WEB_INFO", `{ "message": "same" }`, key)
				if replayErr != nil || replay != legacy {
					t.Fatalf("replay=%q error=%v", replay, replayErr)
				}
			} else if id != "" {
				t.Fatalf("failed adoption returned id %q", id)
			}
			assertNotificationCounts(t, repo, user, 1, map[bool]int{true: 1, false: 0}[want == codes.OK])
		})
	}
}

func assertNotificationCounts(t *testing.T, repo *Repository, user string, wantRows, wantLedger int) {
	t.Helper()
	var rows, ledger int
	if err := repo.pg.QueryRow(t.Context(), `SELECT count(*) FROM notifications WHERE user_id=$1`, user).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := repo.pg.QueryRow(t.Context(), `SELECT count(*) FROM command_idempotency_keys WHERE scope=$1`, "user:"+user).Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	if rows != wantRows || ledger != wantLedger {
		t.Fatalf("rows=%d ledger=%d want %d/%d", rows, ledger, wantRows, wantLedger)
	}
}

func TestIntegrationNotificationCommandFailuresLeaveNoWrites(t *testing.T) {
	repo := newTestRepository(t, "ALL", 20)
	user := seedNotificationContractUser(t, repo)
	for _, tc := range []struct {
		user, payload, key string
		code               codes.Code
	}{
		{"bad-user", `{}`, uuid.NewString(), codes.InvalidArgument},
		{user, `[]`, uuid.NewString(), codes.InvalidArgument},
		{user, `{}`, "", codes.InvalidArgument},
		{uuid.NewString(), `{}`, uuid.NewString(), codes.Internal},
	} {
		id, err := repo.CreateNotification(t.Context(), tc.user, "WEB_INFO", tc.payload, tc.key)
		if id != "" || status.Code(err) != tc.code {
			t.Fatalf("id=%q error=%v want %s", id, err, tc.code)
		}
	}
	assertNotificationCounts(t, repo, user, 0, 0)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if id, err := repo.CreateNotification(ctx, user, "WEB_INFO", `{}`, uuid.NewString()); id != "" || status.Code(err) != codes.Internal {
		t.Fatalf("canceled begin id=%q error=%v", id, err)
	}
}

func TestIntegrationNotificationInsertHelperContracts(t *testing.T) {
	repo := newTestRepository(t, "ALL", 20)
	user := seedNotificationContractUser(t, repo)
	hash, err := notificationRequestHash(user, "WEB_INFO", `{"a":1}`)
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	for _, payload := range []string{`{"a":1}`, `{"a":2}`, `[]`} {
		tx, txErr := repo.pg.BeginTx(t.Context())
		if txErr != nil {
			t.Fatal(txErr)
		}
		if _, execErr := tx.Exec(t.Context(), `INSERT INTO notifications(user_id,kind,payload,idempotency_key) VALUES($1,'WEB_INFO',$2,$3)`, user, payload, key); execErr != nil {
			t.Fatal(execErr)
		}
		id, insertErr := repo.insertNotification(t.Context(), tx, user, "WEB_INFO", `{"a":1}`, key, hash)
		want := codes.OK
		if payload == `{"a":2}` {
			want = codes.AlreadyExists
		}
		if payload == `[]` {
			want = codes.Internal
		}
		if status.Code(insertErr) != want || (want == codes.OK && id == "") || (want != codes.OK && id != "") {
			t.Fatalf("payload=%s id=%q error=%v want %s", payload, id, insertErr, want)
		}
		if rollbackErr := tx.Rollback(t.Context()); rollbackErr != nil {
			t.Fatal(rollbackErr)
		}
	}

	assertNotificationCounts(t, repo, user, 0, 0)
}

func TestIntegrationNotificationReadOwnershipAndErrors(t *testing.T) {
	repo := newTestRepository(t, "ALL", 20)
	user := seedNotificationContractUser(t, repo)
	id, err := repo.CreateNotification(t.Context(), user, "WEB_INFO", `{}`, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ids  []string
		user string
		code codes.Code
	}{
		{nil, user, codes.InvalidArgument},
		{[]string{"bad"}, user, codes.InvalidArgument},
		{[]string{id}, "bad-user", codes.InvalidArgument},
		{[]string{id}, uuid.NewString(), codes.NotFound},
		{[]string{id, uuid.NewString()}, user, codes.NotFound},
	} {
		if readErr := repo.MarkNotificationsRead(t.Context(), tc.ids, tc.user); status.Code(readErr) != tc.code {
			t.Fatalf("read error=%v want %s", readErr, tc.code)
		}
	}
	var unread bool
	if err = repo.pg.QueryRow(t.Context(), `SELECT read_at IS NULL FROM notifications WHERE id=$1`, id).Scan(&unread); err != nil || !unread {
		t.Fatalf("unread=%v error=%v", unread, err)
	}
	if err = repo.MarkNotificationsRead(t.Context(), []string{id, strings.ToUpper(id)}, user); err != nil {
		t.Fatal(err)
	}
	var first, timeAfter time.Time
	if err = repo.pg.QueryRow(t.Context(), `SELECT read_at FROM notifications WHERE id=$1`, id).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err = repo.MarkNotificationsRead(t.Context(), []string{id}, user); err != nil {
		t.Fatal(err)
	}
	if err = repo.pg.QueryRow(t.Context(), `SELECT read_at FROM notifications WHERE id=$1`, id).Scan(&timeAfter); err != nil || !first.Equal(timeAfter) {
		t.Fatalf("replay changed read time: %v -> %v error=%v", first, timeAfter, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err = repo.MarkNotificationsRead(ctx, []string{id}, user); status.Code(err) != codes.Canceled {
		t.Fatalf("canceled read: %v", err)
	}
}

func TestNotificationReadErrorMappingPreservesDatabaseCause(t *testing.T) {
	repo := newTestRepository(t, "ALL", 20)
	for _, tc := range []struct {
		err     error
		code    codes.Code
		message string
	}{
		{&pgconn.PgError{Code: "22P02", Message: "bad uuid"}, codes.InvalidArgument, "invalid notification IDs"},
		{fmt.Errorf("storage unavailable"), codes.Internal, "failed to validate notification ownership"},
	} {
		err := repo.mapNotificationReadDatabaseError(tc.err, "validate notification ownership")
		if status.Code(err) != tc.code || !strings.Contains(status.Convert(err).Message(), tc.message) {
			t.Fatalf("mapped error=%v", err)
		}
	}
}

// installNotificationFailure scopes a real SQL exception to this test's user.
func installNotificationFailure(t *testing.T, repo *Repository, table, event, predicate string, deferred bool) {
	t.Helper()
	name := "notification_contract_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	function := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF %s THEN RAISE EXCEPTION 'notification contract injected failure'; END IF; RETURN NEW; END $$`, name, predicate)
	if _, err := repo.pg.Exec(t.Context(), function); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := repo.pg.Exec(ctx, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON %s`, name, table)); err != nil {
			t.Errorf("drop trigger: %v", err)
		}
		if _, err := repo.pg.Exec(ctx, fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, name)); err != nil {
			t.Errorf("drop function: %v", err)
		}
	})
	timing := "BEFORE"
	constraint, deferrable := "", ""
	if deferred {
		timing = "AFTER"
		constraint = "CONSTRAINT "
		deferrable = "DEFERRABLE INITIALLY DEFERRED"
	}
	query := fmt.Sprintf(`CREATE %sTRIGGER %s %s %s ON %s %s FOR EACH ROW EXECUTE FUNCTION %s()`, constraint, name, timing, event, table, deferrable, name)
	if _, err := repo.pg.Exec(t.Context(), query); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationNotificationSQLFailuresRollbackCommandAndRead(t *testing.T) {
	for _, stage := range []string{"insert", "complete", "commit", "legacy complete", "legacy commit", "read update", "read commit"} {
		t.Run(stage, func(t *testing.T) {
			repo := newTestRepository(t, "ALL", 20)
			user := seedNotificationContractUser(t, repo)
			key := uuid.NewString()
			legacy := strings.HasPrefix(stage, "legacy")
			reading := strings.HasPrefix(stage, "read")
			var id string
			if legacy || reading {
				if err := repo.pg.QueryRow(t.Context(), `INSERT INTO notifications(user_id,kind,payload,idempotency_key) VALUES($1,'WEB_INFO','{}',$2) RETURNING id`, user, " "+key+" ").Scan(&id); err != nil {
					t.Fatal(err)
				}
			}
			installNotificationStageFailure(t, repo, user, stage)
			if reading {
				err := repo.MarkNotificationsRead(t.Context(), []string{id}, user)
				if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "notification contract injected failure") {
					t.Fatalf("read failure: %v", err)
				}
				var unread bool
				if err := repo.pg.QueryRow(t.Context(), `SELECT read_at IS NULL FROM notifications WHERE id=$1`, id).Scan(&unread); err != nil || !unread {
					t.Fatalf("unread=%v error=%v", unread, err)
				}
			} else {
				got, err := repo.CreateNotification(t.Context(), user, "WEB_INFO", `{}`, key)
				if got != "" || status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "notification contract injected failure") {
					t.Fatalf("id=%q error=%v", got, err)
				}
			}
			rows := 0
			if legacy || reading {
				rows = 1
			}
			assertNotificationCounts(t, repo, user, rows, 0)
		})
	}
}

func TestNotificationConstructorDefaultsAndExplicitRetention(t *testing.T) {
	defaulted := New(nil, nil, nil, nil)
	if defaulted.cfg == nil || defaulted.cfg.EventCommandRetention <= 0 {
		t.Fatalf("default config=%+v", defaulted.cfg)
	}
	cfg := &Config{EventCommandRetention: time.Hour, FetchLimit: 7}
	explicit := New(cfg, nil, nil, nil)
	if explicit.cfg != cfg || explicit.cfg.EventCommandRetention != time.Hour || explicit.cfg.FetchLimit != 7 {
		t.Fatalf("explicit config=%+v", explicit.cfg)
	}
}

func TestIntegrationLegacyNotificationNullPayloadFailsWithoutAdoption(t *testing.T) {
	repo := newTestRepository(t, "ALL", 20)
	user := seedNotificationContractUser(t, repo)
	key := uuid.NewString()
	if _, err := repo.pg.Exec(t.Context(), `INSERT INTO notifications(user_id,kind,payload,idempotency_key) VALUES($1,'WEB_INFO',NULL,$2)`, user, key); err != nil {
		t.Fatal(err)
	}
	id, err := repo.CreateNotification(t.Context(), user, "WEB_INFO", `{}`, key)
	if id != "" || status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "failed to read legacy notification command") {
		t.Fatalf("id=%q error=%v", id, err)
	}
	assertNotificationCounts(t, repo, user, 1, 0)
}

func TestIntegrationNotificationInsertCancellation(t *testing.T) {
	repo := newTestRepository(t, "ALL", 20)
	user := seedNotificationContractUser(t, repo)
	key := uuid.NewString()
	hash, err := notificationRequestHash(user, "WEB_INFO", `{"a":1}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, expired := range []bool{false, true} {
		tx, txErr := repo.pg.BeginTx(t.Context())
		if txErr != nil {
			t.Fatal(txErr)
		}
		ctx, cancel := context.WithCancel(t.Context())
		want := codes.Canceled
		if expired {
			cancel()
			ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			want = codes.DeadlineExceeded
		} else {
			cancel()
		}
		id, insertErr := repo.insertNotification(ctx, tx, user, "WEB_INFO", `{"a":1}`, key, hash)
		cancel()
		if id != "" || status.Code(insertErr) != want {
			t.Fatalf("id=%q error=%v want %s", id, insertErr, want)
		}
		//nolint:errcheck // A canceled query may already close the transaction connection.
		_ = tx.Rollback(t.Context())
	}
	assertNotificationCounts(t, repo, user, 0, 0)
}

func installNotificationStageFailure(t *testing.T, repo *Repository, user, stage string) {
	t.Helper()
	legacy := strings.HasPrefix(stage, "legacy")
	reading := strings.HasPrefix(stage, "read")
	table, event, predicate := "notifications", "INSERT", fmt.Sprintf("NEW.user_id = '%s'::uuid", user)
	deferred := strings.Contains(stage, "commit")
	if strings.Contains(stage, "complete") || legacy {
		table, event = "command_idempotency_keys", "UPDATE"
		predicate = fmt.Sprintf("NEW.scope = 'user:%s'", user)
	}
	if reading {
		event = "UPDATE"
	}
	installNotificationFailure(t, repo, table, event, predicate, deferred)
}

func TestIntegrationNotificationKeyConflictPreservesOriginal(t *testing.T) {
	repo := newTestRepository(t, "ALL", 20)
	user := seedNotificationContractUser(t, repo)
	key := uuid.NewString()
	original, err := repo.CreateNotification(t.Context(), user, "WEB_INFO", `{"message":"original"}`, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct{ kind, payload string }{
		{"WEB_INFO", `{"message":"changed"}`},
		{"WEB_ALERT", `{"message":"original"}`},
	} {
		id, createErr := repo.CreateNotification(t.Context(), user, request.kind, request.payload, key)
		if id != "" || status.Code(createErr) != codes.AlreadyExists {
			t.Fatalf("conflict id=%q error=%v", id, createErr)
		}
	}
	replay, err := repo.CreateNotification(t.Context(), user, "WEB_INFO", `{"message":"original"}`, key)
	if err != nil || replay != original {
		t.Fatalf("original replay=%q error=%v", replay, err)
	}
	assertNotificationCounts(t, repo, user, 1, 1)
}

func seedNotificationContractUser(t *testing.T, repo *Repository) string {
	t.Helper()
	return testkit.SeedUser(t.Context(), t, repo.pg, "notification-contract-"+uuid.NewString()+"@chronoverse.test")
}
