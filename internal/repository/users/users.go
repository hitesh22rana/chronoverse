package users

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jackc/pgx/v5"

	usersmodel "github.com/hitesh22rana/chronoverse/internal/model/users"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	userTable = "users"
)

// Repository provides users repository.
type Repository struct {
	tp   trace.Tracer
	auth auth.IAuth
	pg   *postgres.Postgres
}

// New creates a new auth repository.
func New(auth auth.IAuth, pg *postgres.Postgres) *Repository {
	return &Repository{
		tp:   otel.Tracer(svcpkg.Info().GetName()),
		auth: auth,
		pg:   pg,
	}
}

// RegisterUser a new user.
//
//nolint:gocritic // ID and authToken are returned.
func (r *Repository) RegisterUser(ctx context.Context, email, password string) (ID, authToken string, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.RegisterUser")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		err = status.Errorf(codes.Internal, "failed to hash password: %v", err)
		return "", "", err
	}

	// Insert user into database
	query := fmt.Sprintf(`
		INSERT INTO %s (email, password) 
		VALUES ($1, $2)
		RETURNING id;
	`, userTable)

	err = r.pg.QueryRow(ctx, query, email, string(hashedPassword)).Scan(&ID)
	if err != nil {
		// Check if the user already exists
		if r.pg.IsUniqueViolation(err) {
			err = status.Errorf(codes.AlreadyExists, "user already exists: %v", err)
			return "", "", err
		}

		err = status.Errorf(codes.Internal, "failed to insert user: %v", err)
		return "", "", err
	}

	// Issue authToken
	authToken, err = r.auth.IssueToken(ctx, ID)
	if err != nil {
		return "", "", err
	}

	return ID, authToken, nil
}

// LoginUser user.
//
//nolint:gocritic // ID and authToken are returned.
func (r *Repository) LoginUser(ctx context.Context, email, pass string) (ID, authToken string, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.LoginUser")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Fetch user from database
	var password string
	query := fmt.Sprintf(`
		SELECT id, password
		FROM %s WHERE email = $1
		LIMIT 1;
	`, userTable)

	err = r.pg.QueryRow(ctx, query, email).Scan(&ID, &password)
	if err != nil {
		if r.pg.IsNoRows(err) {
			err = status.Errorf(codes.NotFound, "user not found: %v", err)
			return "", "", err
		} else if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid email: %v", err)
			return "", "", err
		}

		err = status.Errorf(codes.Internal, "failed to fetch user: %v", err)
		return "", "", err
	}

	// Validate password
	if err = bcrypt.CompareHashAndPassword([]byte(password), []byte(pass)); err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid password: %v", err)
		return "", "", err
	}

	// Issue authToken
	authToken, err = r.auth.IssueToken(ctx, ID)
	if err != nil {
		return "", "", err
	}

	return ID, authToken, nil
}

// GetUser fetches user by ID.
func (r *Repository) GetUser(ctx context.Context, id string) (res *usersmodel.GetUserResponse, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.GetUser")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	query := fmt.Sprintf(`
		SELECT id, email, notification_preference, created_at, updated_at
		FROM %s WHERE id = $1
		LIMIT 1;
	`, userTable)

	//nolint:errcheck // The error is handled in the next line
	rows, _ := r.pg.Query(ctx, query, id)
	res, err = pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByName[usersmodel.GetUserResponse])
	if err != nil {
		if r.pg.IsNoRows(err) {
			err = status.Errorf(codes.NotFound, "user not found: %v", err)
			return nil, err
		} else if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid user ID: %v", err)
			return nil, err
		}

		err = status.Errorf(codes.Internal, "failed to fetch user: %v", err)
		return nil, err
	}

	return res, nil
}

// UpdateUser updates the user.
func (r *Repository) UpdateUser(ctx context.Context, id, password, notificationPreference string) (err error) {
	ctx, span := r.tp.Start(ctx, "Repository.UpdateUser")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		err = status.Errorf(codes.Internal, "failed to hash password: %v", err)
		return err
	}

	query := fmt.Sprintf(`
		UPDATE %s
		SET password = $1, notification_preference = $2
		WHERE id = $3;
	`, userTable)
	args := []any{string(hashedPassword), notificationPreference, id}

	// Execute the query
	ct, err := r.pg.Exec(ctx, query, args...)
	if err != nil {
		if r.pg.IsNoRows(err) {
			err = status.Errorf(codes.NotFound, "user not found: %v", err)
			return err
		} else if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid user ID: %v", err)
			return err
		}

		err = status.Errorf(codes.Internal, "failed to update user: %v", err)
		return err
	}

	if ct.RowsAffected() == 0 {
		err = status.Errorf(codes.NotFound, "user not found: %v", err)
		return err
	}

	return nil
}
