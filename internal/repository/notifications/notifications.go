package notifications

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	notificationsmodel "github.com/hitesh22rana/chronoverse/internal/model/notifications"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	notificationsTable = "notifications"
	delimiter          = '$'
)

// Config represents the repository constants configuration.
type Config struct {
	FetchLimit int
}

// Repository provides notifications repository.
type Repository struct {
	tp  trace.Tracer
	cfg *Config
	pg  *postgres.Postgres
}

// New creates a new notifications repository.
func New(cfg *Config, pg *postgres.Postgres) *Repository {
	return &Repository{
		tp:  otel.Tracer(svcpkg.Info().GetName()),
		cfg: cfg,
		pg:  pg,
	}
}

// CreateNotification creates a new notification.
func (r *Repository) CreateNotification(ctx context.Context, userID, kind, payload string) (notificationID string, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.CreateNotification")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	query := fmt.Sprintf(`
		INSERT INTO %s (user_id, kind, payload)
		VALUES ($1, $2, $3)
		RETURNING id;
	`, notificationsTable)

	err = r.pg.QueryRow(ctx, query, userID, kind, payload).Scan(&notificationID)
	if err != nil {
		err = status.Errorf(codes.Internal, "failed to create notification: %v", err)
		return "", err
	}

	return notificationID, nil
}

// MarkNotificationsRead marks all notifications as read.
func (r *Repository) MarkNotificationsRead(ctx context.Context, ids []string, userID string) (err error) {
	ctx, span := r.tp.Start(ctx, "Repository.MarkNotificationsRead")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	query := fmt.Sprintf(`
		UPDATE %s
		SET read_at = COALESCE(read_at, NOW())
		WHERE id = ANY($1) AND user_id = $2
	`, notificationsTable)

	ct, err := r.pg.Exec(ctx, query, ids, userID)
	if err != nil {
		if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid notification ID's: %v", err)
			return err
		}

		err = status.Errorf(codes.Internal, "failed to mark all notifications as read: %v", err)
		return err
	}

	if ct.RowsAffected() == 0 {
		err = status.Errorf(codes.NotFound, "notifications not found")
		return err
	}

	return nil
}

// ListNotifications returns notifications by user ID.
// By default, it only returns the unread notifications.
func (r *Repository) ListNotifications(ctx context.Context, userID, kind, cursor string) (res *notificationsmodel.ListNotificationsResponse, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.ListNotifications")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	query := fmt.Sprintf(`
		SELECT id, kind, payload, read_at, created_at, updated_at
		FROM %s
		WHERE user_id = $1 AND read_at IS NULL
	`, notificationsTable)
	args := []any{userID}

	// Add kind filter if provided
	if kind != "" {
		query += ` AND kind = $2`
		args = append(args, kind)
	}

	// Add cursor pagination
	if cursor != "" {
		id, createdAt, _err := extractDataFromCursor(cursor)
		if _err != nil {
			err = _err
			return nil, err
		}

		if kind != "" {
			query += ` AND (created_at, id) <= ($3, $4)`
		} else {
			query += ` AND (created_at, id) <= ($2, $3)`
		}
		args = append(args, createdAt, id)
	}

	query += fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT %d;`, r.cfg.FetchLimit+1)

	//nolint:errcheck // The error is handled in the next line
	rows, _ := r.pg.Query(ctx, query, args...)
	data, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[notificationsmodel.NotificationResponse])
	if err != nil {
		if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid user ID: %v", err)
			return nil, err
		}

		err = status.Errorf(codes.Internal, "failed to list all notifications: %v", err)
		return nil, err
	}

	// Check if there are more notifications
	cursor = ""
	if len(data) > r.cfg.FetchLimit {
		cursor = fmt.Sprintf(
			"%s%c%s",
			data[r.cfg.FetchLimit].ID,
			delimiter,
			data[r.cfg.FetchLimit].CreatedAt.Format(time.RFC3339Nano),
		)
		data = data[:r.cfg.FetchLimit]
	}

	return &notificationsmodel.ListNotificationsResponse{
		Notifications: data,
		Cursor:        encodeCursor(cursor),
	}, nil
}

func encodeCursor(cursor string) string {
	if cursor == "" {
		return ""
	}
	return base64.StdEncoding.EncodeToString([]byte(cursor))
}

func extractDataFromCursor(cursor string) (string, time.Time, error) {
	parts := bytes.Split([]byte(cursor), []byte{delimiter})
	if len(parts) != 2 {
		return "", time.Time{}, status.Error(codes.InvalidArgument, "invalid cursor: expected two parts")
	}

	createdAt, err := time.Parse(time.RFC3339Nano, string(parts[1]))
	if err != nil {
		return "", time.Time{}, status.Errorf(codes.InvalidArgument, "invalid timestamp: %v", err)
	}

	return string(parts[0]), createdAt, nil
}
