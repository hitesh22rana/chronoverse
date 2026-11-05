//nolint:testpackage // Exercises repository error translation through real PostgreSQL calls.
package notifications

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestIntegrationListNotificationsMapsDatabaseInputAndContextErrors(t *testing.T) {
	const userID = "00000000-0000-4000-8000-000000000011"
	cases := []struct {
		name, user, cursor string
		code               codes.Code
	}{
		{name: "canceled", user: userID, code: codes.Canceled},
		{name: "expired", user: userID, code: codes.DeadlineExceeded},
		{name: "invalid owner", user: "not-a-uuid", code: codes.InvalidArgument},
		{name: "invalid cursor identity", user: userID, cursor: "not-a-uuid$2026-11-05T00:00:00Z", code: codes.InvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newTestRepository(t, "ALL", 20)
			ctx := t.Context()
			switch tc.name {
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "expired":
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
			}
			response, err := repo.ListNotifications(ctx, tc.user, tc.cursor)
			if response != nil || status.Code(err) != tc.code {
				t.Fatalf("response=%+v error=%v, want nil/%s", response, err, tc.code)
			}
		})
	}
}

func TestIntegrationListNotificationsMapsUnavailablePoolToInternal(t *testing.T) {
	repo := newTestRepository(t, "ALL", 20)
	cfg, err := pgx.ParseConfig(testkit.PostgresDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	dedicated, err := postgres.New(t.Context(), &postgres.Config{
		Host: cfg.Host, Port: int(cfg.Port), User: cfg.User, Password: cfg.Password, Database: cfg.Database,
		MaxConns: 1, TLSConfig: &postgres.TLSConfig{Enabled: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	dedicated.Close()
	repo.pg = dedicated
	response, err := repo.ListNotifications(t.Context(), "00000000-0000-4000-8000-000000000011", "")
	if response != nil || status.Code(err) != codes.Internal {
		t.Fatalf("response=%+v error=%v, want nil/Internal", response, err)
	}
	if !strings.Contains(status.Convert(err).Message(), "closed pool") {
		t.Fatalf("error=%v, want closed-pool cause", err)
	}
}
