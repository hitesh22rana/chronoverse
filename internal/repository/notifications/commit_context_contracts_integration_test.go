//nolint:testpackage // Cancels an actual pgx COMMIT through the tracing boundary.
package notifications

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

type (
	notificationCommitCancelKey       struct{}
	notificationCommitCancelProcessor struct{}
)

// OTel invokes OnStart synchronously before pgx sends the COMMIT query.
func (*notificationCommitCancelProcessor) OnStart(ctx context.Context, span sdktrace.ReadWriteSpan) {
	if cancel, ok := ctx.Value(notificationCommitCancelKey{}).(context.CancelFunc); ok && span.Name() == "COMMIT" {
		cancel()
	}
}
func (*notificationCommitCancelProcessor) OnEnd(sdktrace.ReadOnlySpan)      {}
func (*notificationCommitCancelProcessor) Shutdown(context.Context) error   { return nil }
func (*notificationCommitCancelProcessor) ForceFlush(context.Context) error { return nil }

// The test is serial because it temporarily installs a recording provider.
func newNotificationCommitCancellationRepository(t *testing.T) *Repository {
	t.Helper()
	cfg, err := pgx.ParseConfig(testkit.PostgresDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	previous := otel.GetTracerProvider()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(&notificationCommitCancelProcessor{}))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if shutdownErr := provider.Shutdown(ctx); shutdownErr != nil {
			t.Errorf("shutdown commit tracer: %v", shutdownErr)
		}
	})
	pg, err := postgres.New(t.Context(), &postgres.Config{
		Host: cfg.Host, Port: int(cfg.Port), User: cfg.User, Password: cfg.Password, Database: cfg.Database,
		MaxConns: 1, TLSConfig: &postgres.TLSConfig{},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Cleanup ordering closes this provider's pool before restoring the provider.
	t.Cleanup(pg.Close)
	repo := newTestRepository(t, "ALL", 20)
	repo.pg = pg
	return repo
}

func TestIntegrationNotificationReplayCommitCancellationPreservesCommand(t *testing.T) {
	repo := newNotificationCommitCancellationRepository(t)
	user := seedNotificationContractUser(t, repo)
	key := uuid.NewString()
	original, err := repo.CreateNotification(t.Context(), user, "WEB_INFO", `{"message":"original"}`, key)
	if err != nil {
		t.Fatal(err)
	}
	before := readNotificationCommandSnapshot(t, repo, user, key)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx = context.WithValue(ctx, notificationCommitCancelKey{}, cancel)
	id, err := repo.CreateNotification(ctx, user, "WEB_INFO", `{"message":"original"}`, key)
	if id != "" || status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "failed to commit notification replay") || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("replay id=%q error=%v", id, err)
	}
	if ctx.Err() != context.Canceled {
		t.Fatalf("caller was not canceled at COMMIT: %v", ctx.Err())
	}
	if after := readNotificationCommandSnapshot(t, repo, user, key); after != before {
		t.Fatalf("failed replay changed notification/ledger: %q -> %q", before, after)
	}
	retry, err := repo.CreateNotification(t.Context(), user, "WEB_INFO", `{"message":"original"}`, key)
	if err != nil || retry != original {
		t.Fatalf("retry id=%q error=%v, want %q", retry, err, original)
	}
	if after := readNotificationCommandSnapshot(t, repo, user, key); after != before {
		t.Fatalf("retry changed notification/ledger: %q -> %q", before, after)
	}
	assertNotificationCounts(t, repo, user, 1, 1)
}

func readNotificationCommandSnapshot(t *testing.T, repo *Repository, user, key string) [2]string {
	t.Helper()
	var snapshot [2]string
	if err := repo.pg.QueryRow(t.Context(), `SELECT row_to_json(notification)::text FROM notifications AS notification
 WHERE user_id=$1 AND idempotency_key=$2`, user, key).Scan(&snapshot[0]); err != nil {
		t.Fatal(err)
	}
	if err := repo.pg.QueryRow(t.Context(), `SELECT row_to_json(command)::text FROM command_idempotency_keys AS command
 WHERE scope=$1 AND operation='notification.create' AND idempotency_key=$2`, "user:"+user, key).Scan(&snapshot[1]); err != nil {
		t.Fatal(err)
	}
	return snapshot
}
