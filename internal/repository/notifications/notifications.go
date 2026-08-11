package notifications

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	userspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"

	notificationsmodel "github.com/hitesh22rana/chronoverse/internal/model/notifications"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	authSubject = "internal/notifications"
	delimiter   = '$'
)

// Services represents the services used by the notifications repository.
type Services struct {
	UsersService userspb.UsersServiceClient
}

// Config represents the repository constants configuration.
type Config struct {
	FetchLimit int
}

// Repository provides notifications repository.
type Repository struct {
	tp   trace.Tracer
	cfg  *Config
	auth auth.IAuth
	pg   *postgres.Postgres
	svc  *Services
}

// New creates a new notifications repository.
func New(cfg *Config, auth auth.IAuth, pg *postgres.Postgres, svc *Services) *Repository {
	return &Repository{
		tp:   otel.Tracer(svcpkg.Info().GetName()),
		cfg:  cfg,
		auth: auth,
		pg:   pg,
		svc:  svc,
	}
}

// CreateNotification creates a new notification.
func (r *Repository) CreateNotification(ctx context.Context, userID, kind, payload, idempotencyKey string) (notificationID string, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.CreateNotification")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to start notification transaction: %v", err)
	}
	//nolint:errcheck // Rollback is a no-op after commit.
	defer tx.Rollback(ctx)

	requestHash, err := notificationRequestHash(userID, kind, payload)
	if err != nil {
		return "", err
	}
	idempotencyKey, err = commandidempotency.NormalizeKey(idempotencyKey)
	if err != nil {
		return "", err
	}
	scope := commandidempotency.UserScope(userID)
	reservation, err := commandidempotency.Reserve(ctx, tx, scope, commandidempotency.OperationNotificationCreate, idempotencyKey, requestHash)
	if err != nil {
		return "", err
	}
	if reservation.Replay {
		if err = tx.Commit(ctx); err != nil {
			return "", status.Errorf(codes.Internal, "failed to commit notification replay: %v", err)
		}
		return reservation.ResourceID, nil
	}
	notificationID, legacyReplay, err := r.adoptLegacyNotificationCommand(
		ctx, tx, userID, idempotencyKey, requestHash,
	)
	if err != nil {
		return "", err
	}
	if legacyReplay {
		if err = tx.Commit(ctx); err != nil {
			return "", status.Errorf(codes.Internal, "failed to commit legacy notification replay: %v", err)
		}
		return notificationID, nil
	}

	notificationID, err = r.insertNotification(ctx, tx, userID, kind, payload, idempotencyKey, requestHash)
	if err != nil {
		return "", err
	}

	if completeErr := commandidempotency.Complete(
		ctx, tx, scope, commandidempotency.OperationNotificationCreate,
		idempotencyKey, requestHash, notificationID, map[string]string{"id": notificationID}, true,
	); completeErr != nil {
		return "", completeErr
	}
	if err = tx.Commit(ctx); err != nil {
		return "", status.Errorf(codes.Internal, "failed to commit notification command: %v", err)
	}
	return notificationID, nil
}

func (r *Repository) insertNotification(
	ctx context.Context,
	tx pgx.Tx,
	userID,
	kind,
	payload,
	idempotencyKey,
	requestHash string,
) (notificationID string, err error) {
	query := fmt.Sprintf(`
		INSERT INTO %s (user_id, kind, payload, idempotency_key)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, idempotency_key)
		WHERE idempotency_key IS NOT NULL
		DO UPDATE SET idempotency_key = EXCLUDED.idempotency_key
		RETURNING id, kind, payload;
	`, postgres.TableNotifications)
	var storedKind, storedPayload string
	err = tx.QueryRow(ctx, query, userID, kind, payload, idempotencyKey).Scan(
		&notificationID,
		&storedKind,
		&storedPayload,
	)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return "", status.Error(codes.DeadlineExceeded, err.Error())
		case errors.Is(err, context.Canceled):
			return "", status.Error(codes.Canceled, err.Error())
		default:
			return "", status.Errorf(codes.Internal, "failed to create notification: %v", err)
		}
	}
	if err := validateStoredNotificationCommand(requestHash, userID, storedKind, storedPayload); err != nil {
		return "", err
	}
	return notificationID, nil
}

func (r *Repository) adoptLegacyNotificationCommand(
	ctx context.Context,
	tx pgx.Tx,
	userID,
	idempotencyKey,
	requestHash string,
) (notificationID string, found bool, err error) {
	query := fmt.Sprintf(`
		SELECT id, kind, payload
		FROM %s
		WHERE user_id = $1
		  AND idempotency_key IS NOT NULL
		  AND btrim(idempotency_key, ' ') = $2
		FOR UPDATE
		LIMIT 1;
	`, postgres.TableNotifications)
	var storedKind, storedPayload string
	err = tx.QueryRow(ctx, query, userID, idempotencyKey).Scan(
		&notificationID,
		&storedKind,
		&storedPayload,
	)
	if r.pg.IsNoRows(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, status.Errorf(codes.Internal, "failed to read legacy notification command: %v", err)
	}
	if validationErr := validateStoredNotificationCommand(requestHash, userID, storedKind, storedPayload); validationErr != nil {
		return "", false, validationErr
	}
	if completeErr := commandidempotency.Complete(
		ctx,
		tx,
		commandidempotency.UserScope(userID),
		commandidempotency.OperationNotificationCreate,
		idempotencyKey,
		requestHash,
		notificationID,
		map[string]string{"id": notificationID},
		true,
	); completeErr != nil {
		return "", false, completeErr
	}
	return notificationID, true, nil
}

func validateStoredNotificationCommand(requestHash, userID, kind, payload string) error {
	storedRequestHash, err := notificationRequestHash(userID, kind, payload)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to hash stored notification command: %v", err)
	}
	if storedRequestHash != requestHash {
		return status.Error(codes.AlreadyExists, "idempotency key was already used with a different request")
	}
	return nil
}

func notificationRequestHash(userID, kind, payload string) (string, error) {
	canonicalPayload, err := idempotency.CanonicalJSONObject(payload)
	if err != nil {
		return "", status.Errorf(codes.InvalidArgument, "invalid notification payload: %v", err)
	}
	hash, err := idempotency.HashCanonical(map[string]string{
		"user_id": userID,
		"kind":    kind,
		"payload": string(canonicalPayload),
	})
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to hash notification command: %v", err)
	}
	return hash, nil
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

	unique := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return status.Error(codes.InvalidArgument, "at least one notification ID is required")
	}
	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to start notification-read transaction: %v", err)
	}
	//nolint:errcheck // Rollback is a no-op after commit.
	defer tx.Rollback(ctx)

	var owned int
	query := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE id = ANY($1) AND user_id = $2`, postgres.TableNotifications)
	if err = tx.QueryRow(ctx, query, unique, userID).Scan(&owned); err != nil {
		if r.pg.IsInvalidTextRepresentation(err) {
			return status.Errorf(codes.InvalidArgument, "invalid notification IDs: %v", err)
		}
		return status.Errorf(codes.Internal, "failed to validate notification ownership: %v", err)
	}
	if owned != len(unique) {
		return status.Error(codes.NotFound, "one or more notifications were not found")
	}

	query = fmt.Sprintf(`
		UPDATE %s
		SET read_at = clock_timestamp() AT TIME ZONE 'utc'
		WHERE id = ANY($1) AND user_id = $2 AND read_at IS NULL
	`, postgres.TableNotifications)

	_, err = tx.Exec(ctx, query, unique, userID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = status.Error(codes.DeadlineExceeded, err.Error())
			return err
		} else if errors.Is(err, context.Canceled) {
			err = status.Error(codes.Canceled, err.Error())
			return err
		}

		if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid notification ID's: %v", err)
			return err
		}

		err = status.Errorf(codes.Internal, "failed to mark all notifications as read: %v", err)
		return err
	}

	return tx.Commit(ctx)
}

// ListNotifications returns notifications by user ID.
// By default, it only returns the unread notifications.
func (r *Repository) ListNotifications(ctx context.Context, userID, cursor string) (res *notificationsmodel.ListNotificationsResponse, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.ListNotifications")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Issue necessary headers and tokens for authorization
	ctx, ctxErr := r.withAuthorization(ctx)
	if ctxErr != nil {
		err = ctxErr
		return nil, err
	}

	// Fetch user details for user's preferences
	user, usersErr := r.svc.UsersService.GetUser(ctx, &userspb.GetUserRequest{
		Id: userID,
	})
	if usersErr != nil {
		err = usersErr
		return nil, err
	}

	var notificationsPreferences []string
	switch user.GetNotificationPreference() {
	case "ALL":
		notificationsPreferences = []string{
			notificationsmodel.KindWebAlert.ToString(),
			notificationsmodel.KindWebError.ToString(),
			notificationsmodel.KindWebWarn.ToString(),
			notificationsmodel.KindWebInfo.ToString(),
			notificationsmodel.KindWebSuccess.ToString(),
		}
	case "ALERTS":
		notificationsPreferences = []string{
			notificationsmodel.KindWebAlert.ToString(),
		}
	case "NONE":
		// If the user has opted out of notifications, return an empty response
		return &notificationsmodel.ListNotificationsResponse{
			Notifications: nil,
			Cursor:        "",
		}, nil
	default:
		// If the user has an invalid preference, return error
		err = status.Errorf(codes.InvalidArgument, "invalid notification preference: %s", user.GetNotificationPreference())
		return nil, err
	}

	query := fmt.Sprintf(`
        SELECT id, kind, payload, read_at, created_at, updated_at
        FROM %s
        WHERE user_id = $1 AND read_at IS NULL AND kind = ANY($2)
    `, postgres.TableNotifications)
	args := []any{userID, notificationsPreferences}

	// Add cursor pagination
	if cursor != "" {
		id, createdAt, _err := extractDataFromCursor(cursor)
		if _err != nil {
			err = _err
			return nil, err
		}

		query += ` AND (created_at, id) <= ($3, $4)`
		args = append(args, createdAt, id)
	}

	query += fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT %d;`, r.cfg.FetchLimit+1)

	rows, err := r.pg.Query(ctx, query, args...)
	if errors.Is(err, context.DeadlineExceeded) {
		err = status.Error(codes.DeadlineExceeded, err.Error())
		return nil, err
	} else if errors.Is(err, context.Canceled) {
		err = status.Error(codes.Canceled, err.Error())
		return nil, err
	}

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

// withAuthorization issues the necessary headers and tokens for authorization.
func (r *Repository) withAuthorization(ctx context.Context) (context.Context, error) {
	return auth.WithInternalServiceAuthorization(ctx, r.auth, authSubject)
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
